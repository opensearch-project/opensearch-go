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

	"github.com/google/renameio/v2/maybe"
	"github.com/stretchr/testify/suite"
)

// generateSuiteMu serializes suites that mutate the global repoRoot.
var generateSuiteMu sync.Mutex

// GenerateSuite tests the generatePaths and generateAPI functions with
// repoRoot overridden to point at a temporary directory.
type GenerateSuite struct {
	suite.Suite

	tmpDir     string
	origRootFn func() (string, error)
}

func TestGenerateSuite(t *testing.T) {
	suite.Run(t, new(GenerateSuite))
}

func (s *GenerateSuite) SetupSuite() {
	generateSuiteMu.Lock()

	s.tmpDir = s.T().TempDir()
	s.origRootFn = repoRoot
	repoRoot = func() (string, error) { return s.tmpDir, nil }
}

func (s *GenerateSuite) TearDownSuite() {
	repoRoot = s.origRootFn
	generateSuiteMu.Unlock()
}

func (s *GenerateSuite) TestGeneratePaths() {
	specPath := buildTestSpec(s.T())
	outFile := filepath.Join(s.tmpDir, "builders_gen.go")
	testOutFile := filepath.Join(s.tmpDir, "builders_gen_test.go")

	err := generatePaths(specPath, nil, "path", outFile, testOutFile, VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	src, err := os.ReadFile(outFile)
	s.Require().NoError(err)
	s.Require().Contains(string(src), "package path")
	s.Require().Contains(string(src), "ClusterHealthPath")
	s.Require().Contains(string(src), "IndicesRefreshPath")

	testSrc, err := os.ReadFile(testOutFile)
	s.Require().NoError(err)
	s.Require().Contains(string(testSrc), "package path")
	s.Require().Contains(string(testSrc), "TestClusterHealthPath_Build")
}

func (s *GenerateSuite) TestGeneratePaths_Filter() {
	specPath := buildTestSpec(s.T())
	outFile := filepath.Join(s.tmpDir, "filter_builders_gen.go")

	filter := map[string]bool{"cluster.health": true}
	err := generatePaths(specPath, filter, "path", outFile, "", VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	src, err := os.ReadFile(outFile)
	s.Require().NoError(err)
	s.Require().Contains(string(src), "ClusterHealthPath")
	s.Require().NotContains(string(src), "IndicesRefreshPath")
}

func (s *GenerateSuite) TestGeneratePaths_Stdout() {
	specPath := buildTestSpec(s.T())

	err := generatePaths(specPath, nil, "path", "", "", VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)
}

func (s *GenerateSuite) TestGeneratePaths_InvalidSpec() {
	err := generatePaths("/nonexistent/spec.yaml", nil, "path", "", "", VersionRange{}, BreadcrumbConfig{})
	s.Require().Error(err)
}

func (s *GenerateSuite) TestGenerateAPI() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api")
	pluginsDir := filepath.Join(s.tmpDir, "plugins")

	err := generateAPI(specPath, nil, outDir, pluginsDir, opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	entries, err := os.ReadDir(outDir)
	s.Require().NoError(err)
	s.Require().NotEmpty(entries)

	var foundClusterHealth bool
	for _, e := range entries {
		if e.Name() == "cluster-health_gen.go" {
			foundClusterHealth = true
			src, err := os.ReadFile(filepath.Join(outDir, e.Name()))
			s.Require().NoError(err)
			s.Require().Contains(string(src), "ClusterHealthReq")
			s.Require().Contains(string(src), "GET /_cluster/health")
		}
	}
	s.Require().True(foundClusterHealth, "expected cluster-health_gen.go in output")
}

func (s *GenerateSuite) TestGenerateAPI_Filter() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api-filter")

	filter := map[string]bool{"cluster.health": true}
	err := generateAPI(specPath, filter, outDir, "", opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	entries, err := os.ReadDir(outDir)
	s.Require().NoError(err)

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.Require().Contains(names, "cluster-health_gen.go")
	s.Require().NotContains(names, "indices-refresh_gen.go")
}

func (s *GenerateSuite) TestGenerateAPI_InvalidSpec() {
	err := generateAPI(
		"/nonexistent/spec.yaml",
		nil,
		filepath.Join(s.tmpDir, "invalid"),
		"",
		opensearchAPIPkgName,
		VersionRange{},
		BreadcrumbConfig{})
	s.Require().Error(err)
}

func (s *GenerateSuite) TestGenerateAPI_WithPlugins() {
	specPath := buildTestSpecWithPlugin(s.T())
	outDir := filepath.Join(s.tmpDir, "api-plugins")
	pluginsDir := filepath.Join(s.tmpDir, "plugins-with")

	err := generateAPI(specPath, nil, outDir, pluginsDir, opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	pluginDir := filepath.Join(pluginsDir, "knn")
	entries, err := os.ReadDir(pluginDir)
	s.Require().NoError(err)
	s.Require().NotEmpty(entries)

	var foundCompat bool
	for _, e := range entries {
		if e.Name() == "compat_gen.go" {
			foundCompat = true
		}
	}
	s.Require().True(foundCompat, "expected compat_gen.go in plugin dir")
}

func (s *GenerateSuite) TestGenerateAPI_RemovesStaleFiles() {
	specPath := buildTestSpec(s.T())
	outDir := filepath.Join(s.tmpDir, "api-stale")
	s.Require().NoError(os.MkdirAll(outDir, 0o755))

	// Plant a stale generated file.
	staleFile := filepath.Join(outDir, "old-operation_gen.go")
	s.Require().NoError(maybe.WriteFile(staleFile, []byte("package "+opensearchAPIPkgName+"\n"), 0o600))

	err := generateAPI(specPath, nil, outDir, "", opensearchAPIPkgName, VersionRange{}, BreadcrumbConfig{})
	s.Require().NoError(err)

	// Stale file should be removed.
	_, err = os.Stat(staleFile)
	s.Require().True(os.IsNotExist(err), "stale file should have been removed")

	// Fresh files should exist.
	entries, err := os.ReadDir(outDir)
	s.Require().NoError(err)
	s.Require().NotEmpty(entries)
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
