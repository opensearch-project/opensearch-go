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

	// modulePath is the Go module path for this project.
	modulePath = "github.com/opensearch-project/opensearch-go/v4"

	// opensearchAPIImport is the full import path for the core API package.
	opensearchAPIImport = modulePath + "/" + opensearchAPIPkgName

	// pluginsImportBase is the import path prefix for plugin packages.
	pluginsImportBase = modulePath + "/plugins"

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
	"_common":          true,
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

// importPath returns the full Go import path for the package that owns a
// given operation group. When corePkg differs from opensearchAPIPkgName, it
// uses that as the core package path segment.
func importPath(group string) string {
	return importPathForPkg(group, opensearchAPIPkgName)
}

func importPathForPkg(group, corePkg string) string {
	prefix := groupPrefix(group)
	if coreGroups[prefix] {
		if corePkg != opensearchAPIPkgName {
			return modulePath + "/" + corePkg
		}
		return opensearchAPIImport
	}
	if corePkg != opensearchAPIPkgName {
		return modulePath + "/" + corePkg + "/plugins/" + prefix
	}
	return pluginsImportBase + "/" + prefix
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

// dispatchRoute describes how an operation group maps to a dispatch method.
type dispatchRoute struct {
	ReceiverType string // Go type name (e.g. "clusterClient", "Client")
	MethodName   string // exported method name (e.g. "Health")
	TopLevel     bool   // true for Client, false for sub-clients
	Deprecated   bool   // true for nested sub-client forwarding methods
}

// subClientInfo describes a sub-client type and its placement in the hierarchy.
type subClientInfo struct {
	TypeName  string // e.g. "catClient"
	FieldName string // exported field on parent (e.g. "Cat")
	Parent    string // parent client type ("Client" or "indicesClient")
}

// nestedSubClientOverrides maps operation group names to deprecated sub-client
// forwarding methods. These are the nested sub-client methods that provide
// backward compatibility (e.g. client.Indices.Alias.Get) alongside the
// canonical flat methods (e.g. client.Indices.GetAlias).
var nestedSubClientOverrides = map[string]dispatchRoute{
	"indices.get_alias":            {ReceiverType: "aliasClient", MethodName: "Get", Deprecated: true},
	"indices.put_alias":            {ReceiverType: "aliasClient", MethodName: "Put", Deprecated: true},
	"indices.delete_alias":         {ReceiverType: "aliasClient", MethodName: "Delete", Deprecated: true},
	"indices.exists_alias":         {ReceiverType: "aliasClient", MethodName: "Exists", Deprecated: true},
	"indices.get_mapping":          {ReceiverType: "mappingClient", MethodName: "Get", Deprecated: true},
	"indices.put_mapping":          {ReceiverType: "mappingClient", MethodName: "Put", Deprecated: true},
	"indices.get_field_mapping":    {ReceiverType: "mappingClient", MethodName: "Field", Deprecated: true},
	"indices.get_settings":         {ReceiverType: "settingsClient", MethodName: "Get", Deprecated: true},
	"indices.put_settings":         {ReceiverType: "settingsClient", MethodName: "Put", Deprecated: true},
	"snapshot.create_repository":   {ReceiverType: "repositoryClient", MethodName: "Create", Deprecated: true},
	"snapshot.delete_repository":   {ReceiverType: "repositoryClient", MethodName: "Delete", Deprecated: true},
	"snapshot.get_repository":      {ReceiverType: "repositoryClient", MethodName: "Get", Deprecated: true},
	"snapshot.verify_repository":   {ReceiverType: "repositoryClient", MethodName: "Verify", Deprecated: true},
	"snapshot.cleanup_repository":  {ReceiverType: "repositoryClient", MethodName: "Cleanup", Deprecated: true},
}

// subClientHierarchy defines the sub-client types and their nesting.
var subClientHierarchy = []subClientInfo{
	{TypeName: "catClient", FieldName: "Cat", Parent: "Client"},
	{TypeName: "clusterClient", FieldName: "Cluster", Parent: "Client"},
	{TypeName: "danglingClient", FieldName: "Dangling", Parent: "Client"},
	{TypeName: "documentClient", FieldName: "Document", Parent: "Client"},
	{TypeName: "indicesClient", FieldName: "Indices", Parent: "Client"},
	{TypeName: "aliasClient", FieldName: "Alias", Parent: "indicesClient"},
	{TypeName: "mappingClient", FieldName: "Mapping", Parent: "indicesClient"},
	{TypeName: "settingsClient", FieldName: "Settings", Parent: "indicesClient"},
	{TypeName: "nodesClient", FieldName: "Nodes", Parent: "Client"},
	{TypeName: "scriptClient", FieldName: "Script", Parent: "Client"},
	{TypeName: "componentTemplateClient", FieldName: "ComponentTemplate", Parent: "Client"},
	{TypeName: "indexTemplateClient", FieldName: "IndexTemplate", Parent: "Client"},
	{TypeName: "templateClient", FieldName: "Template", Parent: "Client"},
	{TypeName: "dataStreamClient", FieldName: "DataStream", Parent: "Client"},
	{TypeName: "pointInTimeClient", FieldName: "PointInTime", Parent: "Client"},
	{TypeName: "ingestClient", FieldName: "Ingest", Parent: "Client"},
	{TypeName: "tasksClient", FieldName: "Tasks", Parent: "Client"},
	{TypeName: "scrollClient", FieldName: "Scroll", Parent: "Client"},
	{TypeName: "searchPipelineClient", FieldName: "SearchPipeline", Parent: "Client"},
	{TypeName: "snapshotClient", FieldName: "Snapshot", Parent: "Client"},
	{TypeName: "repositoryClient", FieldName: "Repository", Parent: "snapshotClient"},
}

// prefixToReceiverType maps a group prefix to its primary (flat) receiver type.
var prefixToReceiverType = map[string]string{
	"cat":              "catClient",
	"cluster":          "clusterClient",
	"dangling_indices": "danglingClient",
	"indices":          "indicesClient",
	"nodes":            "nodesClient",
	"snapshot":         "snapshotClient",
	"tasks":            "tasksClient",
	"ingest":           "ingestClient",
	"search_pipeline":  "searchPipelineClient",
	"scroll":           "scrollClient",
	"remote_store":     "Client",
}

// unprefixedGroupOverrides routes prefix-less operation groups (no dot) that
// would otherwise resolve to top-level Client methods, to a specific sub-client
// to avoid field/method name collisions or maintain backward compatibility.
var unprefixedGroupOverrides = map[string]dispatchRoute{
	"scroll":       {ReceiverType: "scrollClient", MethodName: "Get", TopLevel: false},
	"clear_scroll": {ReceiverType: "scrollClient", MethodName: "Delete", TopLevel: false},
}

// resolveDispatchRoutes returns all dispatch routes for an operation group.
// The first route is always the canonical flat method on the prefix's client.
// If the operation has a nested sub-client override, a second deprecated
// forwarding route is appended.
// Returns nil for plugin operations (which have their own client types).
func resolveDispatchRoutes(group string) []dispatchRoute {
	prefix := groupPrefix(group)

	if !coreGroups[prefix] {
		return nil
	}

	primary := resolvePrimaryDispatch(group, prefix)
	routes := []dispatchRoute{primary}

	if nested, ok := nestedSubClientOverrides[group]; ok {
		routes = append(routes, nested)
	}

	return routes
}

// resolvePrimaryDispatch returns the canonical flat dispatch route.
func resolvePrimaryDispatch(group, prefix string) dispatchRoute {
	// Check for explicit overrides of prefix-less groups first.
	if override, ok := unprefixedGroupOverrides[group]; ok {
		return override
	}

	if prefix == "" || prefix == "_core" || prefix == "_common" {
		var name string
		if prefix == "_core" {
			name = group[len("_core."):]
		} else {
			name = group
		}
		return dispatchRoute{
			ReceiverType: "Client",
			MethodName:   methodNameFromSuffix(name),
			TopLevel:     true,
		}
	}

	receiver, ok := prefixToReceiverType[prefix]
	if !ok {
		receiver = prefix + "Client"
	}

	suffix := group
	if idx := strings.IndexByte(group, '.'); idx >= 0 {
		suffix = group[idx+1:]
	}

	return dispatchRoute{
		ReceiverType: receiver,
		MethodName:   methodNameFromSuffix(suffix),
		TopLevel:     receiver == "Client",
	}
}

// methodNameFromSuffix converts an operation suffix to a PascalCase method name.
// e.g. "health" -> "Health", "get_weighted_routing" -> "GetWeightedRouting"
func methodNameFromSuffix(suffix string) string {
	parts := strings.FieldsFunc(suffix, func(r rune) bool {
		return r == '_'
	})
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(titleSegment(p))
	}
	return sb.String()
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
