// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"net/http"
	"path/filepath"
	"strings"
)

const (
	// opensearchAPIPkgName is the Go package name for core API types.
	opensearchAPIPkgName = "opensearchapi"

	genFileSuffix     = "_gen.go"
	genTestFileSuffix = "_gen_test.go"

	// envSkipGitCheck disables the git-toplevel safety check when set to a
	// truthy value (per strconv.ParseBool). Useful in CI environments where
	// the generator runs outside a git working tree.
	envSkipGitCheck = "OSGEN_SKIP_GIT_CHECK"
)

// coreGroups are operation-group prefixes that belong in opensearchapi/.
var coreGroups = map[string]bool{
	"":                 true,
	"_core":            true,
	"cat":              true,
	"cluster":          true,
	"indices":          true,
	"nodes":            true,
	"snapshot":         true,
	"tasks":            true,
	"ingest":           true,
	"dangling_indices": true,
	"search_pipeline":  true,
	"scroll":           true,
	"remote_store":     true,
}

// routeOperation determines the Go package and output directory for an operation.
func routeOperation(group, outDir, pluginsDir string) (pkg, dir string) {
	prefix := groupPrefix(group)
	if coreGroups[prefix] {
		return opensearchAPIPkgName, outDir
	}
	return prefix, filepath.Join(pluginsDir, prefix)
}

// groupPrefix returns the part before the first dot, or "" for unprefixed groups.
func groupPrefix(group string) string {
	if idx := strings.IndexByte(group, '.'); idx >= 0 {
		return group[:idx]
	}
	return ""
}

// operationFilename returns the base filename (without .gen.go extension) for a
// generated operation file. The caller appends ".gen.go" or ".gen_test.go".
//
// Core operations use a hyphenated group name:
//
//	"indices.create" -> "indices-create"
//	"search"         -> "search"
//	"_core.search"   -> "search"
//
// Plugin operations use the leaf operation name:
//
//	"ism.add_policy" -> "add_policy"
//	"knn.stats"      -> "stats"
func operationFilename(group string) string {
	prefix := groupPrefix(group)
	if coreGroups[prefix] {
		if prefix == "_core" {
			return group[len("_core."):]
		}
		if prefix != "" {
			return strings.Replace(group, ".", "-", 1)
		}
		return group
	}
	if idx := strings.IndexByte(group, '.'); idx >= 0 {
		return group[idx+1:]
	}
	return group
}

// httpMethodConst converts a method string to the Go net/http constant name.
func httpMethodConst(method string) string {
	switch strings.ToUpper(method) {
	case http.MethodGet:
		return "http.MethodGet"
	case http.MethodPost:
		return "http.MethodPost"
	case http.MethodPut:
		return "http.MethodPut"
	case http.MethodDelete:
		return "http.MethodDelete"
	case http.MethodHead:
		return "http.MethodHead"
	case http.MethodPatch:
		return "http.MethodPatch"
	case http.MethodOptions:
		return "http.MethodOptions"
	case http.MethodTrace:
		return "http.MethodTrace"
	case http.MethodConnect:
		return "http.MethodConnect"
	default:
		return "http.MethodGet"
	}
}
