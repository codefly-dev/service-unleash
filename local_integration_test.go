package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	runnersbase "github.com/codefly-dev/core/runners/base"
	dockerrun "github.com/codefly-dev/core/runners/dockerrun"
	"github.com/stretchr/testify/require"
)

func TestLocalCompositionEvaluatesControlledFlagThroughEdge(t *testing.T) {
	if os.Getenv("CODEFLY_UNLEASH_INTEGRATION") != "1" {
		t.Skip("set CODEFLY_UNLEASH_INTEGRATION=1 to run the Docker lifecycle test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	postgresHostPort := integrationPort(t)
	adminHostPort := integrationPort(t)
	edgeHostPort := integrationPort(t)
	postgresPassword := "integration-postgres-password"
	adminToken := "*:*.integration-admin-token"
	clientToken := "default:development.integration-client-token"
	components := localComposition(postgresHostPort, adminHostPort, edgeHostPort, postgresPassword, adminToken, clientToken)

	var runners []*dockerrun.DockerEnvironment
	defer func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), time.Minute)
		defer shutdownCancel()
		for index := len(runners) - 1; index >= 0; index-- {
			require.NoError(t, runners[index].Shutdown(shutdownContext))
		}
	}()

	for _, component := range components {
		runner, err := dockerrun.NewDockerHeadlessEnvironment(
			ctx,
			component.image,
			fmt.Sprintf("unleash-integration-%d-%s", time.Now().UnixNano(), component.name),
		)
		require.NoError(t, err)
		runner.WithEphemeral()
		runner.WithPortMapping(ctx, component.hostPort, component.containerPort)
		runner.WithEnvironmentVariables(ctx, component.environment...)
		if len(component.command) > 0 {
			runner.WithCommand(component.command...)
		}
		runners = append(runners, runner)
		require.NoError(t, runner.Init(ctx))

		switch component.name {
		case "postgres":
			require.NoError(t, waitForTCP(ctx, fmt.Sprintf("127.0.0.1:%d", postgresHostPort), 90*time.Second))
		case "server":
			require.NoError(t, waitForHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/health", adminHostPort), 2*time.Minute))
		case "edge":
			require.NoError(t, waitForHTTP(ctx, fmt.Sprintf("http://127.0.0.1:%d/internal-backstage/ready", edgeHostPort), time.Minute))
		}
	}

	adminURL := fmt.Sprintf("http://127.0.0.1:%d", adminHostPort)
	edgeURL := fmt.Sprintf("http://127.0.0.1:%d", edgeHostPort)
	flagName := "codefly-controlled-flag"
	integrationJSONRequest(t, http.MethodPost, adminURL+"/api/admin/projects/default/features", adminToken, map[string]any{"name": flagName}, http.StatusCreated)
	integrationJSONRequest(t, http.MethodPost, adminURL+"/api/admin/projects/default/features/"+flagName+"/environments/development/strategies", adminToken, map[string]any{"name": "default"}, http.StatusOK)
	integrationJSONRequest(t, http.MethodPost, adminURL+"/api/admin/projects/default/features/"+flagName+"/environments/development/on", adminToken, map[string]any{}, http.StatusOK)

	require.Eventually(t, func() bool {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, edgeURL+"/api/client/features", nil)
		if err != nil {
			return false
		}
		request.Header.Set("Authorization", clientToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return false
		}
		var payload struct {
			Features []struct {
				Name       string `json:"name"`
				Enabled    bool   `json:"enabled"`
				Strategies []struct {
					Name string `json:"name"`
				} `json:"strategies"`
			} `json:"features"`
		}
		if json.NewDecoder(response.Body).Decode(&payload) != nil {
			return false
		}
		for _, feature := range payload.Features {
			if feature.Name == flagName && feature.Enabled && len(feature.Strategies) == 1 && feature.Strategies[0].Name == "default" {
				return true
			}
		}
		return false
	}, 45*time.Second, time.Second)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, edgeURL+"/api/admin/projects/default/features", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", adminToken)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.NotContains(t, []int{http.StatusOK, http.StatusCreated, http.StatusNoContent}, response.StatusCode)
}

func integrationPort(t *testing.T) uint16 {
	t.Helper()
	port, err := runnersbase.FindFreePort()
	require.NoError(t, err)
	return uint16(port)
}

func integrationJSONRequest(t *testing.T, method, address, token string, payload any, expectedStatus int) {
	t.Helper()
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	request, err := http.NewRequestWithContext(context.Background(), method, address, bytes.NewReader(encoded))
	require.NoError(t, err)
	request.Header.Set("Authorization", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equalf(t, expectedStatus, response.StatusCode, "%s %s: %s", method, address, content)
}
