// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir

// Canonical module path and core-API location used by the generator and
// referenced by tests. Centralized here so a future relocation (e.g. when
// the v5 branch promotes v5preview/opensearchapi to the module root) only
// requires editing this file.
const (
	// ModulePath is the Go module path for opensearch-go.
	ModulePath = "github.com/opensearch-project/opensearch-go/v4"

	// DefaultCorePkgName is the Go package name for core API types.
	DefaultCorePkgName = "opensearchapi"

	// DefaultCoreSubpath is the relative path (within the module) where the
	// core API package lives. Currently nested under v5preview/ during the
	// v4 -> v5 transition.
	DefaultCoreSubpath = "v5preview/" + DefaultCorePkgName

	// DefaultCoreImportPath is the full Go import path for the core API package.
	DefaultCoreImportPath = ModulePath + "/" + DefaultCoreSubpath

	// DefaultPluginsImportBase is the import-path prefix for plugin packages.
	DefaultPluginsImportBase = DefaultCoreImportPath + "/plugins"
)
