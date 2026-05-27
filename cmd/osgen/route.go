// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
)

const (
	// opensearchAPIPkgName is the Go package name for core API types.
	opensearchAPIPkgName = "opensearchapi"

	// modulePath is the Go module path for this project.
	modulePath = "github.com/opensearch-project/opensearch-go/v4"

	// opensearchAPIImport is the full import path for the core API package.
	opensearchAPIImport = modulePath + "/" + opensearchAPIPkgName

	// pluginsImportBase is the import path prefix for plugin packages when
	// using the default package name. When -pkg overrides the package name,
	// importPathForPkg computes the path from corePkg directly.
	pluginsImportBase = modulePath + "/" + opensearchAPIPkgName + "/plugins"

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
	return prefix, path.Join(pluginsDir, prefix)
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
// Panics on an unknown method so a spec typo or a new HTTP verb fails the
// generator loudly instead of silently emitting MethodGet.
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
		panic(fmt.Sprintf("osgen: unknown HTTP method %q in spec; add a case to httpMethodConst", method))
	}
}

// pluginSubClientInfo describes a sub-client within a plugin package.
type pluginSubClientInfo struct {
	TypeName  string // e.g. "actionGroupClient"
	FieldName string // exported field on Client (e.g. "ActionGroup")
}

// pluginSubClientResult holds the derived sub-client hierarchy for one plugin.
type pluginSubClientResult struct {
	SubClients []pluginSubClientInfo
	// Assignment maps operation group (e.g. "security.get_action_group") to the
	// sub-client FieldName it belongs to, or "" for root Client operations.
	Assignment map[string]string
}

// resolvePluginSubClients derives sub-client groupings for a set of plugin
// operations sharing a package. Operations are grouped by resource noun
// extracted from their x-operation-group suffix. Resources with 2+ operations
// get a sub-client; single-operation resources stay flat on root Client.
func resolvePluginSubClients(groups []string) pluginSubClientResult {
	type opInfo struct {
		group    string // full group (e.g. "security.get_action_group")
		suffix   string // part after dot (e.g. "get_action_group")
		resource string // normalized resource noun (e.g. "action_group")
	}

	var ops []opInfo
	resourceOps := make(map[string][]string) // resource -> list of groups

	for _, g := range groups {
		suffix := g
		if idx := strings.IndexByte(g, '.'); idx >= 0 {
			suffix = g[idx+1:]
		}

		resource := extractResourceNoun(suffix)
		if resource == "" {
			ops = append(ops, opInfo{group: g, suffix: suffix})
			continue
		}

		canonical := normalizeNoun(resource)
		ops = append(ops, opInfo{group: g, suffix: suffix, resource: canonical})
		resourceOps[canonical] = append(resourceOps[canonical], g)
	}

	// Build sub-clients for resources with 2+ operations.
	subClientMap := make(map[string]pluginSubClientInfo)
	for resource, opGroups := range resourceOps {
		if len(opGroups) < 2 {
			continue
		}
		subClientMap[resource] = pluginSubClientInfo{
			TypeName:  resourceToTypeName(resource),
			FieldName: resourceToFieldName(resource),
		}
	}

	// Collect sub-clients sorted for deterministic output.
	scNames := make([]string, 0, len(subClientMap))
	for resource := range subClientMap {
		scNames = append(scNames, resource)
	}
	sort.Strings(scNames)

	result := pluginSubClientResult{
		Assignment: make(map[string]string, len(ops)),
	}
	for _, resource := range scNames {
		result.SubClients = append(result.SubClients, subClientMap[resource])
	}

	// Assign each operation to its sub-client (or "" for root).
	for _, op := range ops {
		if sc, ok := subClientMap[op.resource]; ok {
			result.Assignment[op.group] = sc.FieldName
		}
	}

	return result
}

// verbPrefixes are common verb prefixes in operation suffixes, ordered
// longest-first so longer prefixes match before shorter ones.
var verbPrefixes = []string{
	"create_update_",
	"get_all_",
	"delete_",
	"create_",
	"update_",
	"search_",
	"execute_",
	"deploy_",
	"undeploy_",
	"register_",
	"deregister_",
	"generate_",
	"simulate_",
	"predict_",
	"reload_",
	"upload_",
	"unload_",
	"change_",
	"chunk_",
	"train_",
	"flush_",
	"load_",
	"post_",
	"patch_",
	"list_",
	"add_",
	"get_",
	"put_",
}

// extractResourceNoun strips the leading verb prefix from an operation suffix
// and returns the remainder as the resource noun. Returns "" if no known verb
// prefix matches (the operation stays flat on root Client).
func extractResourceNoun(suffix string) string {
	for _, vp := range verbPrefixes {
		if strings.HasPrefix(suffix, vp) {
			remainder := suffix[len(vp):]
			if remainder != "" {
				return remainder
			}
		}
	}
	return ""
}

// normalizeNoun singularizes each underscore-separated word in a resource noun
// so that plural variants group with their singular form.
func normalizeNoun(noun string) string {
	words := strings.Split(noun, "_")
	for i, w := range words {
		words[i] = singularize(w)
	}
	return strings.Join(words, "_")
}

// singularize attempts to convert a plural English word to singular.
func singularize(word string) string {
	if len(word) <= 2 {
		return word
	}
	if s, ok := irregularSingulars[word]; ok {
		return s
	}
	switch {
	case strings.HasSuffix(word, "ies"):
		return word[:len(word)-3] + "y"
	case strings.HasSuffix(word, "sses"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "xes") ||
		strings.HasSuffix(word, "zes") ||
		strings.HasSuffix(word, "shes"):
		return word[:len(word)-2]
	case strings.HasSuffix(word, "ches"):
		// "batches" -> "batch" (ch + es), but "caches" -> "cache" (silent-e + s).
		// English orthography offers no general rule that distinguishes the two
		// from spelling alone (both have "ch" before "es"), so the irregular
		// map above carries the silent-e cases we actually encounter.
		return word[:len(word)-2]
	case strings.HasSuffix(word, "ses"):
		// "aliases" -> "alias" (strip "es"), but "responses" -> handled by
		// the sses rule above.
		return word[:len(word)-2]
	case strings.HasSuffix(word, "ss") ||
		strings.HasSuffix(word, "us") ||
		strings.HasSuffix(word, "is"):
		return word
	case strings.HasSuffix(word, "s"):
		return word[:len(word)-1]
	default:
		return word
	}
}

// irregularSingulars maps plural English words to their singular form for
// cases where suffix-based rules produce the wrong result. Keep this list
// minimal and limited to nouns that appear in OpenAPI resource paths.
var irregularSingulars = map[string]string{
	"caches":  "cache",
	"niches":  "niche",
	"indices": "index",
}

// resourceToTypeName converts a resource noun to an unexported Go client type
// name. e.g. "action_group" -> "actionGroupClient".
func resourceToTypeName(resource string) string {
	parts := strings.Split(resource, "_")
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, p := range parts[1:] {
		sb.WriteString(titleSegment(p))
	}
	sb.WriteString("Client")
	return sb.String()
}

// resourceToFieldName converts a resource noun to an exported Go field name.
// e.g. "action_group" -> "ActionGroup".
func resourceToFieldName(resource string) string {
	return methodNameFromSuffix(resource)
}
