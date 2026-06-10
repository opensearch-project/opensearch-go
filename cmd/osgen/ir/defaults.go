// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir

// Canonical module path and core-API location used by the generator and
// referenced by tests. Centralized here so a future relocation only requires
// editing this file.
const (
	// ModulePath is the Go module path for opensearch-go.
	ModulePath = "github.com/opensearch-project/opensearch-go/v5"

	// DefaultCorePkgName is the Go package name for core API types.
	DefaultCorePkgName = "opensearchapi"

	// DefaultCoreSubpath is the relative path (within the module) where the
	// core API package lives.
	DefaultCoreSubpath = DefaultCorePkgName

	// DefaultCoreImportPath is the full Go import path for the core API package.
	DefaultCoreImportPath = ModulePath + "/" + DefaultCoreSubpath

	// DefaultPluginsSubpath is the relative path (within the module) where the
	// generated plugin packages live. Plugins are siblings of the core package
	// at the module root, not nested under it.
	DefaultPluginsSubpath = "plugins"

	// DefaultPluginsImportBase is the import-path prefix for plugin packages.
	DefaultPluginsImportBase = ModulePath + "/" + DefaultPluginsSubpath
)
