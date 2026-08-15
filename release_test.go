package main

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestReleaseBuildTargets(t *testing.T) {
	data, err := os.ReadFile(".goreleaser.yaml")
	require.NoError(t, err)
	var configuration struct {
		Builds []struct {
			Environment      []string `yaml:"env"`
			OperatingSystems []string `yaml:"goos"`
			Architectures    []string `yaml:"goarch"`
		} `yaml:"builds"`
	}
	require.NoError(t, yaml.Unmarshal(data, &configuration))
	var targets []string
	for _, build := range configuration.Builds {
		require.Contains(t, build.Environment, "CGO_ENABLED=0")
		for _, operatingSystem := range build.OperatingSystems {
			for _, architecture := range build.Architectures {
				targets = append(targets, operatingSystem+"/"+architecture)
			}
		}
	}
	sort.Strings(targets)
	require.Equal(t, []string{"darwin/amd64", "darwin/arm64", "linux/amd64"}, targets)
}

func TestAgentAndReleaseManifest(t *testing.T) {
	data, err := os.ReadFile("agent.codefly.yaml")
	require.NoError(t, err)
	var manifest struct {
		Publisher string `yaml:"publisher"`
		Kind      string `yaml:"kind"`
		Name      string `yaml:"name"`
		Version   string `yaml:"version"`
	}
	require.NoError(t, yaml.Unmarshal(data, &manifest))
	require.Equal(t, "codefly.dev", manifest.Publisher)
	require.Equal(t, "codefly:service", manifest.Kind)
	require.Equal(t, "unleash", manifest.Name)
	require.Equal(t, "0.0.0", manifest.Version)

	workflow, err := os.ReadFile(".github/workflows/releaser.yml")
	require.NoError(t, err)
	require.Contains(t, string(workflow), "go-service-release.yml@main")
	guard, err := os.ReadFile(".github/workflows/manifest-guard.yml")
	require.NoError(t, err)
	require.Contains(t, string(guard), "plugin-manifest-guard.yml@main")
}

func TestDocumentedImagesMatchRuntimePins(t *testing.T) {
	documentation, err := os.ReadFile("VERSIONS.md")
	require.NoError(t, err)
	for _, image := range []*resources.DockerImage{serverImage, edgeImage, postgresImage} {
		for _, pin := range []string{image.Name, image.Tag, image.Digest} {
			require.Truef(t, strings.Contains(string(documentation), pin), "VERSIONS.md does not document %s", pin)
		}
	}
}
