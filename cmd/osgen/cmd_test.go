// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// generateSuiteMu serializes suites that mutate the global repoRootFunc.
var generateSuiteMu sync.Mutex

// GenerateSuite tests the generatePaths and generateAPI functions with
// repoRootFunc overridden to point at a temporary directory.
type GenerateSuite struct {
	suite.Suite

	tmpDir      string
	origRootFn  func() (string, error)
}

func TestGenerateSuite(t *testing.T) {
	suite.Run(t, new(GenerateSuite))
}

func (s *GenerateSuite) SetupSuite() {
	generateSuiteMu.Lock()

	s.tmpDir = s.T().TempDir()
	s.origRootFn = repoRootFunc
	repoRootFunc = func() (string, error) { return s.tmpDir, nil }
}

func (s *GenerateSuite) TearDownSuite() {
	repoRootFunc = s.origRootFn
	generateSuiteMu.Unlock()
}

func (s *GenerateSuite) TestGeneratePaths() {
	specPath := buildTestSpec(s.T())
	outFile := filepath.Join(s.tmpDir, "builders_gen.go")
	testOutFile := filepath.Join(s.tmpDir, "builders_gen_test.go")

	err := generatePaths(specPath, nil, "path", outFile, testOutFile, VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	src, err := os.ReadFile(outFile)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(src), "package path")
	require.Contains(s.T(), string(src), "ClusterHealthPath")
	require.Contains(s.T(), string(src), "IndicesRefreshPath")

	testSrc, err := os.ReadFile(testOutFile)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(testSrc), "package path")
	require.Contains(s.T(), string(testSrc), "TestClusterHealthPath_Build")
}

func (s *GenerateSuite) TestGeneratePaths_Filter() {
	specPath := buildTestSpec(s.T())
	outFile := filepath.Join(s.tmpDir, "filter_builders_gen.go")

	filter := map[string]bool{"cluster.health": true}
	err := generatePaths(specPath, filter, "path", outFile, "", VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	src, err := os.ReadFile(outFile)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(src), "ClusterHealthPath")
	require.NotContains(s.T(), string(src), "IndicesRefreshPath")
}

func (s *GenerateSuite) TestGeneratePaths_Stdout() {
	specPath := buildTestSpec(s.T())

	err := generatePaths(specPath, nil, "path", "", "", VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)
}

func (s *GenerateSuite) TestGeneratePaths_InvalidSpec() {
	err := generatePaths("/nonexistent/spec.yaml", nil, "path", "", "", VersionRange{}, BreadcrumbConfig{})
	require.Error(s.T(), err)
}

func (s *GenerateSuite) TestGenerateAPI() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api")
	pluginsDir := filepath.Join(s.tmpDir, "plugins")

	err := generateAPI(specPath, nil, outDir, pluginsDir, opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	entries, err := os.ReadDir(outDir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), entries)

	var foundClusterHealth bool
	for _, e := range entries {
		if e.Name() == "cluster-health_gen.go" {
			foundClusterHealth = true
			src, err := os.ReadFile(filepath.Join(outDir, e.Name()))
			require.NoError(s.T(), err)
			require.Contains(s.T(), string(src), "ClusterHealthReq")
			require.Contains(s.T(), string(src), "GET /_cluster/health")
		}
	}
	require.True(s.T(), foundClusterHealth, "expected cluster-health_gen.go in output")
}

func (s *GenerateSuite) TestGenerateAPI_Filter() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api-filter")

	filter := map[string]bool{"cluster.health": true}
	err := generateAPI(specPath, filter, outDir, "", opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	entries, err := os.ReadDir(outDir)
	require.NoError(s.T(), err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.Contains(s.T(), names, "cluster-health_gen.go")
	require.NotContains(s.T(), names, "indices-refresh_gen.go")
}

func (s *GenerateSuite) TestGenerateAPI_InvalidSpec() {
	err := generateAPI("/nonexistent/spec.yaml", nil, filepath.Join(s.tmpDir, "invalid"), "", opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	require.Error(s.T(), err)
}

func (s *GenerateSuite) TestGenerateAPI_WithPlugins() {
	specPath := buildTestSpecWithPlugin(s.T())
	outDir := filepath.Join(s.tmpDir, "api-plugins")
	pluginsDir := filepath.Join(s.tmpDir, "plugins-with")

	err := generateAPI(specPath, nil, outDir, pluginsDir, opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	pluginDir := filepath.Join(pluginsDir, "knn")
	entries, err := os.ReadDir(pluginDir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), entries)

	var foundCompat bool
	for _, e := range entries {
		if e.Name() == "compat_gen.go" {
			foundCompat = true
		}
	}
	require.True(s.T(), foundCompat, "expected compat_gen.go in plugin dir")
}

func (s *GenerateSuite) TestGenerateAPI_RemovesStaleFiles() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api-stale")
	require.NoError(s.T(), os.MkdirAll(outDir, 0o755))

	// Plant a stale generated file.
	staleFile := filepath.Join(outDir, "old-operation_gen.go")
	require.NoError(s.T(), os.WriteFile(staleFile, []byte("package opensearchapi\n"), 0o644))

	err := generateAPI(specPath, nil, outDir, "", opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	require.NoError(s.T(), err)

	// Stale file should be removed.
	_, err = os.Stat(staleFile)
	require.True(s.T(), os.IsNotExist(err), "stale file should have been removed")

	// Fresh files should exist.
	entries, err := os.ReadDir(outDir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), entries)
}

func buildTestSpecWithPlugin(t *testing.T) string {
	t.Helper()
	spec := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Test", "version": "1.0.0"},
		"paths": map[string]any{
			"/_cluster/health": map[string]any{
				"get": map[string]any{
					"x-operation-group": "cluster.health",
					"responses":         map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
			"/_plugins/_knn/stats": map[string]any{
				"get": map[string]any{
					"x-operation-group": "knn.stats",
					"responses":         map[string]any{"200": map[string]any{"description": "OK"}},
				},
			},
		},
	}
	return writeTestSpec(t, spec)
}
