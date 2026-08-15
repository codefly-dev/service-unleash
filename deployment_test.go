package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/codefly-dev/core/agents/services"
	agenttesting "github.com/codefly-dev/core/agents/testing"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func testDeploymentParameters() *deploymentTemplateParameters {
	parameters := defaultDeploymentTemplateParameters()
	parameters.PostgresPasswordReference = &builderv0.KubernetesSecretKeyReference{Name: "unleash-secrets", Key: "postgres-password"}
	parameters.AdminTokenReference = &builderv0.KubernetesSecretKeyReference{Name: "unleash-secrets", Key: "admin-token"}
	parameters.ClientTokenReference = &builderv0.KubernetesSecretKeyReference{Name: "unleash-secrets", Key: "client-token"}
	return parameters
}

func TestDeploymentTemplates(t *testing.T) {
	directory := agenttesting.AssertKustomizeTemplates(t, deploymentFS, testDeploymentParameters())

	deployment := readDeploymentFile(t, directory, "base/deployment.yaml")
	require.Contains(t, deployment, serverImage.FullName())
	require.Contains(t, deployment, edgeImage.FullName())
	require.Contains(t, deployment, "path: /internal-backstage/ready")
	require.Contains(t, deployment, "secretKeyRef:")

	postgres := readDeploymentFile(t, directory, "base/postgres-stateful-set.yaml")
	require.Contains(t, postgres, postgresImage.FullName())
	require.Contains(t, postgres, "kind: StatefulSet")
	require.Contains(t, postgres, "storage: 10Gi")

	service := readDeploymentFile(t, directory, "base/service.yaml")
	require.Contains(t, service, "type: ClusterIP")
	require.Contains(t, service, "name: edge\n      port: 8080")
	require.Contains(t, service, "name: admin\n      port: 80")
	require.NotContains(t, service, "type: LoadBalancer")

	policy := readDeploymentFile(t, directory, "base/network-policy.yaml")
	require.Contains(t, policy, "port: 3063")
	require.Contains(t, policy, "kubernetes.io/metadata.name: \"codefly-test\"")
	require.Contains(t, policy, "port: 4242")
}

func TestDeploymentNeverSerializesSecretValues(t *testing.T) {
	secret := "sentinel-do-not-render"
	directory := renderDeployment(t, builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_EPHEMERAL_LOCAL_APPLY_V1, services.EnvironmentMap{
		"POSTGRES_PASSWORD": "c2VudGluZWwtZG8tbm90LXJlbmRlcg==",
	})
	tree := readManifestTree(t, directory)
	require.NotContains(t, tree, secret)
	require.NotContains(t, tree, "c2VudGluZWwtZG8tbm90LXJlbmRlcg==")
	require.NotContains(t, tree, "kind: Secret")
}

func TestRestrictedDeploymentUsesTypedSecretReferencesAndReturnsValueFreeTokens(t *testing.T) {
	builder, mappings := newDeploymentTestBuilder(t)
	references := deploymentSecretReferences(builder.Unique())
	destination := t.TempDir()
	response, err := builder.Deploy(context.Background(), restrictedDeploymentRequest(destination, mappings, references))
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_SUCCESS, response.GetState().GetState(), response.GetState().GetMessage())

	output := response.GetDeployment().GetKubernetes()
	require.Equal(t, services.KubernetesManifestContractVersion, output.GetContractVersion())
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, output.GetValidation().GetStaticValidation(), output.GetValidation().GetViolations())
	require.NotNil(t, output.GetBundle())
	require.Equal(t, references, output.GetBundle().GetSecretReferences())

	require.Len(t, response.GetConfiguration().GetInfos(), 2)
	for _, information := range response.GetConfiguration().GetInfos() {
		for _, value := range information.GetConfigurationValues() {
			if value.GetKey() == "token" {
				require.True(t, value.GetSecret())
				require.Empty(t, value.GetValue())
			}
		}
	}
	tree := readManifestTree(t, destination)
	require.NotContains(t, tree, "kind: Secret")
	require.NotContains(t, tree, resources.ServiceSecretConfigurationKeyFromUnique(builder.Unique(), configurationName, postgresPasswordKey))
}

func TestRestrictedDeploymentRejectsMissingSecretReferences(t *testing.T) {
	builder, mappings := newDeploymentTestBuilder(t)
	response, err := builder.Deploy(context.Background(), restrictedDeploymentRequest(t.TempDir(), mappings, nil))
	require.NoError(t, err)
	require.Equal(t, builderv0.DeploymentStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "requires a typed Kubernetes Secret reference")
	require.Nil(t, response.GetConfiguration())
}

func TestDeploymentIsDeterministicAndMatchesGolden(t *testing.T) {
	profile := builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1
	first := manifestTreeDigest(t, renderDeployment(t, profile, nil))
	second := manifestTreeDigest(t, renderDeployment(t, profile, nil))
	require.Equal(t, first, second)

	expected, err := os.ReadFile("testdata/golden/deployment.sha256")
	require.NoError(t, err)
	require.Equal(t, strings.TrimSpace(string(expected)), first)
}

func renderDeployment(t *testing.T, profile builderv0.KubernetesOutputProfile, secretMap services.EnvironmentMap) string {
	t.Helper()
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
	destination := t.TempDir()
	deployment := &builderv0.KubernetesDeployment{
		Namespace:   "codefly-test",
		Destination: destination,
		Profile:     profile,
	}
	parameters := services.DeploymentParameters{
		SecretMap:  secretMap,
		Parameters: testDeploymentParameters(),
	}
	require.NoError(t, builder.KustomizeDeploy(ctx, &basev0.Environment{Name: "test"}, deployment, deploymentFS, parameters))
	validation := services.ValidateKubernetesManifestTree(ctx, destination, "test", "codefly-test", profile, false, "", "")
	require.Equal(t, builderv0.KubernetesManifestValidation_STATUS_PASSED, validation.GetStaticValidation(), validation.GetViolations())
	return destination
}

func newDeploymentTestBuilder(t *testing.T) (*Builder, []*basev0.NetworkMapping) {
	t.Helper()
	ctx := context.Background()
	builder := NewBuilder()
	identity := &basev0.ServiceIdentity{Workspace: "workspace", Module: "module", Name: "unleash", Version: "0.0.0"}
	require.NoError(t, builder.HeadlessLoad(ctx, identity))
	builder.Information = &services.Information{
		Service: resources.ToServiceWithCase(builder.Identity),
		Module:  resources.ToModuleWithCase(builder.Identity),
	}
	builder.EnvironmentVariables.SetIdentity(identity)
	builder.adminEndpoint = &basev0.Endpoint{Name: "admin", Module: identity.Module, Service: identity.Name, Api: "tcp", Visibility: resources.VisibilityModule}
	builder.edgeEndpoint = &basev0.Endpoint{Name: "edge", Module: identity.Module, Service: identity.Name, Api: "http", Visibility: resources.VisibilityPublic}
	admin := resources.NewNetworkInstance("unleash.codefly-test.svc.cluster.local", 80)
	admin.Access = resources.NewContainerNetworkAccess()
	edge := resources.NewHTTPNetworkInstance("unleash.codefly-test.svc.cluster.local", 8080, false)
	edge.Access = resources.NewContainerNetworkAccess()
	return builder, []*basev0.NetworkMapping{
		{Endpoint: builder.adminEndpoint, Instances: []*basev0.NetworkInstance{admin}},
		{Endpoint: builder.edgeEndpoint, Instances: []*basev0.NetworkInstance{edge}},
	}
}

func deploymentSecretReferences(unique string) map[string]*builderv0.KubernetesSecretKeyReference {
	return map[string]*builderv0.KubernetesSecretKeyReference{
		resources.ServiceSecretConfigurationKeyFromUnique(unique, configurationName, postgresPasswordKey): {Name: "unleash-secrets", Key: "postgres-password"},
		resources.ServiceSecretConfigurationKeyFromUnique(unique, configurationName, adminTokenKey):       {Name: "unleash-secrets", Key: "admin-token"},
		resources.ServiceSecretConfigurationKeyFromUnique(unique, configurationName, clientTokenKey):      {Name: "unleash-secrets", Key: "client-token"},
	}
}

func restrictedDeploymentRequest(destination string, mappings []*basev0.NetworkMapping, references map[string]*builderv0.KubernetesSecretKeyReference) *builderv0.DeploymentRequest {
	return &builderv0.DeploymentRequest{
		Environment:     &basev0.Environment{Name: "test"},
		NetworkMappings: mappings,
		Deployment: &builderv0.Deployment{Kind: &builderv0.Deployment_Kubernetes{
			Kubernetes: &builderv0.KubernetesDeployment{
				Namespace: destinationNamespace, Destination: destination,
				Profile:          builderv0.KubernetesOutputProfile_KUBERNETES_OUTPUT_PROFILE_RESTRICTED_PORTABLE_V1,
				SecretReferences: references,
			},
		}},
	}
}

const destinationNamespace = "codefly-test"

func readDeploymentFile(t *testing.T, directory, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
	require.NoError(t, err)
	return string(content)
}

func readManifestTree(t *testing.T, directory string) string {
	t.Helper()
	var contents strings.Builder
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		contents.Write(content)
		return nil
	}))
	return contents.String()
}

func manifestTreeDigest(t *testing.T, directory string) string {
	t.Helper()
	var paths []string
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(relative))
		return nil
	}))
	sort.Strings(paths)
	hasher := sha256.New()
	for _, relative := range paths {
		content, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(relative)))
		require.NoError(t, err)
		_, _ = fmt.Fprintf(hasher, "%s\x00", relative)
		_, _ = hasher.Write(content)
		_, _ = hasher.Write([]byte{'\n'})
	}
	return fmt.Sprintf("sha256:%x", hasher.Sum(nil))
}
