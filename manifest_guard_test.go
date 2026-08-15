package main

import (
	"context"
	"os"
	"testing"

	"github.com/codefly-dev/core/agents/services"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestManifestGuardRender(t *testing.T) {
	destination := os.Getenv("CODEFLY_MANIFEST_DESTINATION")
	if destination == "" {
		t.Skip("CODEFLY_MANIFEST_DESTINATION is unset")
	}
	environment := os.Getenv("CODEFLY_MANIFEST_ENVIRONMENT")
	namespace := os.Getenv("CODEFLY_MANIFEST_NAMESPACE")
	profileName := os.Getenv("CODEFLY_MANIFEST_PROFILE")
	require.NotEmpty(t, environment)
	require.NotEmpty(t, namespace)
	profile, ok := builderv0.KubernetesOutputProfile_value[profileName]
	require.Truef(t, ok, "unknown CODEFLY_MANIFEST_PROFILE %q", profileName)

	ctx := context.Background()
	identity := &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "unleash", Version: "0.0.0"}
	base := &services.Base{
		Wool:     wool.Get(ctx),
		Identity: identity,
		Information: &services.Information{
			Service: resources.ToServiceWithCase(identity),
			Module:  resources.ToModuleWithCase(identity),
		},
	}
	base.SetDockerImage(serverImage)
	builder := &services.BuilderWrapper{Base: base}
	base.Builder = builder
	deployment := &builderv0.KubernetesDeployment{
		Namespace:   namespace,
		Destination: destination,
		Profile:     builderv0.KubernetesOutputProfile(profile),
	}
	parameters := services.DeploymentParameters{Parameters: testDeploymentParameters()}
	require.NoError(t, builder.KustomizeDeploy(ctx, &basev0.Environment{Name: environment}, deployment, deploymentFS, parameters))
}
