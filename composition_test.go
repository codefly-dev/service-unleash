package main

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestNewServiceEmbedsBase(t *testing.T) {
	service := NewService()
	require.NotNil(t, service)
	require.NotNil(t, service.Base)
	require.NotNil(t, service.Settings)
}

func TestEndpointExposureContract(t *testing.T) {
	builder := NewBuilder()
	builder.Identity = &resources.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "flags"}
	require.NoError(t, builder.CreateEndpoints(context.Background()))
	require.Len(t, builder.Endpoints, 2)

	require.Equal(t, "admin", builder.adminEndpoint.GetName())
	require.Equal(t, "tcp", builder.adminEndpoint.GetApi())
	require.Equal(t, resources.VisibilityModule, builder.adminEndpoint.GetVisibility())

	require.Equal(t, "edge", builder.edgeEndpoint.GetName())
	require.Equal(t, "http", builder.edgeEndpoint.GetApi())
	require.Equal(t, resources.VisibilityPublic, builder.edgeEndpoint.GetVisibility())
}

func TestLocalCompositionUsesDedicatedPostgresAndEdge(t *testing.T) {
	components := localComposition(15432, 14242, 13063, "database-secret", "admin-secret", "client-secret")
	require.Len(t, components, 3)
	require.Equal(t, []string{"postgres", "server", "edge"}, []string{components[0].name, components[1].name, components[2].name})
	require.Equal(t, postgresImage.FullName(), components[0].image.FullName())
	require.Equal(t, serverImage.FullName(), components[1].image.FullName())
	require.Equal(t, edgeImage.FullName(), components[2].image.FullName())
	require.Equal(t, "/var/lib/postgresql/data", components[0].cacheTarget)
	require.Equal(t, []string{"edge"}, components[2].command)

	serverEnvironment := resources.EnvironmentVariableAsStrings(components[1].environment)
	require.Contains(t, serverEnvironment, "DATABASE_HOST=host.docker.internal")
	require.Contains(t, serverEnvironment, "DATABASE_PORT=15432")
	require.Contains(t, serverEnvironment, "DATABASE_PASSWORD=database-secret")
	edgeEnvironment := resources.EnvironmentVariableAsStrings(components[2].environment)
	require.Contains(t, edgeEnvironment, "UPSTREAM_URL=http://host.docker.internal:14242")
	require.Contains(t, edgeEnvironment, "TOKENS=client-secret")
}
