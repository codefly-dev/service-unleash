package main

import (
	"context"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestCreateAndLoadLifecycle(t *testing.T) {
	ctx := context.Background()
	workspaceRoot := t.TempDir()
	serviceDirectory := filepath.Join(workspaceRoot, "module", "flags")
	service := resources.Service{Name: "flags", Version: "0.0.0"}
	service.WithModule("module")
	require.NoError(t, service.SaveAtDir(ctx, serviceDirectory))
	identity := &basev0.ServiceIdentity{
		Workspace:           "workspace",
		WorkspacePath:       workspaceRoot,
		Module:              "module",
		Name:                "flags",
		Version:             "0.0.0",
		RelativeToWorkspace: "module/flags",
	}

	builder := NewBuilder()
	_, err := builder.Load(ctx, &builderv0.LoadRequest{
		Identity: identity, DisableCatch: true,
		CreationMode: &builderv0.CreationMode{Communicate: false},
	})
	require.NoError(t, err)
	created, err := builder.Create(ctx, &builderv0.CreateRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.CreateStatus_CREATED, created.GetState().GetState(), created.GetState().GetMessage())
	require.Len(t, created.GetEndpoints(), 2)
	require.FileExists(t, filepath.Join(serviceDirectory, "configurations", "local", "unleash.secret.env"))

	runtime := NewRuntime()
	environment := shared.Must(resources.LocalEnvironment().Proto())
	loaded, err := runtime.Load(ctx, &runtimev0.LoadRequest{Identity: identity, Environment: environment, DisableCatch: true})
	require.NoError(t, err)
	require.NotNil(t, loaded)
	require.Len(t, runtime.Endpoints, 2)
	require.Equal(t, resources.VisibilityModule, runtime.adminEndpoint.GetVisibility())
	require.Equal(t, resources.VisibilityPublic, runtime.edgeEndpoint.GetVisibility())
}
