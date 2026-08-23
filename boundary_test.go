package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agenttesting "github.com/codefly-dev/core/agents/testing"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

var pluginOwnedKinds = map[string]struct{}{
	"Namespace":     {},
	"Deployment":    {},
	"StatefulSet":   {},
	"Service":       {},
	"NetworkPolicy": {},
}

func TestManifestsStayWithinProducerBoundary(t *testing.T) {
	directory := agenttesting.AssertKustomizeTemplates(t, deploymentFS, testDeploymentParameters())
	require.NoError(t, filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || (filepath.Ext(path) != ".yaml" && filepath.Ext(path) != ".yml") {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(content)
		for _, forbidden := range []string{"repoURL", "targetRevision", "sourceRef", "argoproj.io", "fluxcd.io"} {
			require.NotContains(t, text, forbidden, path)
		}
		decoder := yaml.NewDecoder(strings.NewReader(text))
		for {
			var document map[string]any
			if err = decoder.Decode(&document); err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			kind, _ := document["kind"].(string)
			if kind == "" || kind == "Kustomization" {
				continue
			}
			_, allowed := pluginOwnedKinds[kind]
			require.Truef(t, allowed, "%s emits unexpected kind %q", path, kind)
		}
		return nil
	}))
}
