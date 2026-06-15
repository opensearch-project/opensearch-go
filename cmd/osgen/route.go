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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

const (
	// opensearchAPIPkgName is the Go package name for core API types.
	opensearchAPIPkgName = ir.DefaultCorePkgName

	// modulePath is the Go module path for this project.
	modulePath = ir.ModulePath

	// opensearchAPIImport is the full import path for the core API package.
	opensearchAPIImport = ir.DefaultCoreImportPath

	// pluginsImportBase is the import path prefix for plugin packages when
	// using the default package name. When -pkg overrides the package name,
	// importPathForPkg computes the path from corePkg directly.
	pluginsImportBase = ir.DefaultPluginsImportBase

	genFileSuffix     = "_gen.go"
	genTestFileSuffix = "_gen_test.go"

	// envSkipGitCheck disables the git-toplevel safety check when set to a
	// truthy value (per strconv.ParseBool). Useful in CI environments where
	// the generator runs outside a git working tree.
	envSkipGitCheck = "OSGEN_SKIP_GIT_CHECK"
)

// coreGroups are operation-group prefixes that belong in opensearchapi/.
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var coreGroups = map[string]bool{
	"":                 true,
	"_common":          true,
	"_core":            true,
	"cat":              true,
	"cluster":          true,
	"dangling_indices": true,
	"indices":          true,
	"ingest":           true,
	"nodes":            true,
	"remote_store":     true,
	"scroll":           true,
	"search_pipeline":  true,
	"snapshot":         true,
	"tasks":            true,
}

// routeOperation determines the Go package and output directory for an operation.
func routeOperation(group, outDir, pluginsDir string) (string, string) {
	prefix := groupPrefix(group)
	if coreGroups[prefix] {
		return opensearchAPIPkgName, outDir
	}
	return prefix, path.Join(pluginsDir, prefix)
}

// importPathForPkg returns the full Go import path for the package that owns
// a given operation group. When corePkg differs from opensearchAPIPkgName, it
// uses that as the core package path segment. Plugin packages are siblings of
// the core package at the module root, independent of the core package name.
func importPathForPkg(group, corePkg string) string {
	prefix := groupPrefix(group)
	if coreGroups[prefix] {
		if corePkg != opensearchAPIPkgName {
			return modulePath + "/" + corePkg
		}
		return opensearchAPIImport
	}
	return pluginsImportBase + "/" + prefix
}

// groupPrefix returns the part before the first dot, or "" for unprefixed groups.
func groupPrefix(group string) string {
	if before, _, ok := strings.Cut(group, "."); ok {
		return before
	}
	return ""
}

// pluginGroupSuffix returns the operation suffix after the plugin prefix
// (e.g. "ism.add_policy" -> "add_policy"), or the whole group when there
// is no dot. It is the input to methodNameFromSuffix for plugin methods.
func pluginGroupSuffix(group string) string {
	if _, after, ok := strings.Cut(group, "."); ok {
		return after
	}
	return group
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
	if _, after, ok := strings.Cut(group, "."); ok {
		return after
	}
	return group
}

// dispatchRoute describes how an operation group maps to a dispatch method.
type dispatchRoute struct {
	ReceiverType string // Go type name (e.g. "clusterClient", "Client")
	MethodName   string // exported method name (e.g. "Health")
	TopLevel     bool   // true for Client, false for sub-clients
	Deprecated   bool   // true for nested sub-client forwarding methods
	// Forward, when non-empty, marks this route as a thin compatibility
	// forwarder: the emitted method body is `return c.<Forward>(ctx, req)`
	// instead of a full dispatch. The expression is relative to the receiver
	// (e.g. "Doc.Bulk" on Client, or "GetSource" on documentClient). The
	// canonical implementation lives on the route this one forwards to.
	Forward string
}

// subClientInfo describes a sub-client type and its placement in the hierarchy.
type subClientInfo struct {
	TypeName  string // e.g. "catClient"
	FieldName string // exported field on parent (e.g. "Cat")
	Parent    string // parent client type ("Client" or "indicesClient")
	// Aliases are additional exported field names on the parent that point at
	// the same sub-client value as FieldName. They exist for compatibility
	// (e.g. "Document" aliasing "Doc", "PointInTime" aliasing "PIT") so callers
	// can reach the sub-client under either name.
	Aliases []string
}

// nestedSubClientOverrides maps operation group names to deprecated sub-client
// forwarding methods. These are the nested sub-client methods that provide
// backward compatibility (e.g. client.Indices.Alias.Get) alongside the
// canonical flat methods (e.g. client.Indices.GetAlias). Keys are sorted
// alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var nestedSubClientOverrides = map[string]dispatchRoute{
	"indices.delete_alias":        {ReceiverType: "aliasClient", MethodName: "Delete", Deprecated: true},
	"indices.exists_alias":        {ReceiverType: "aliasClient", MethodName: "Exists", Deprecated: true},
	"indices.get_alias":           {ReceiverType: "aliasClient", MethodName: "Get", Deprecated: true},
	"indices.get_field_mapping":   {ReceiverType: "mappingClient", MethodName: "Field", Deprecated: true},
	"indices.get_mapping":         {ReceiverType: "mappingClient", MethodName: "Get", Deprecated: true},
	"indices.get_settings":        {ReceiverType: "settingsClient", MethodName: "Get", Deprecated: true},
	"indices.put_alias":           {ReceiverType: "aliasClient", MethodName: "Put", Deprecated: true},
	"indices.put_mapping":         {ReceiverType: "mappingClient", MethodName: "Put", Deprecated: true},
	"indices.put_settings":        {ReceiverType: "settingsClient", MethodName: "Put", Deprecated: true},
	"snapshot.cleanup_repository": {ReceiverType: "repositoryClient", MethodName: "Cleanup", Deprecated: true},
	"snapshot.create_repository":  {ReceiverType: "repositoryClient", MethodName: "Create", Deprecated: true},
	"snapshot.delete_repository":  {ReceiverType: "repositoryClient", MethodName: "Delete", Deprecated: true},
	"snapshot.get_repository":     {ReceiverType: "repositoryClient", MethodName: "Get", Deprecated: true},
	"snapshot.verify_repository":  {ReceiverType: "repositoryClient", MethodName: "Verify", Deprecated: true},
}

// subClientHierarchy defines the sub-client types and their nesting. The
// order is significant: parents must precede children since dispatch
// resolution walks the slice once.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var subClientHierarchy = []subClientInfo{
	{TypeName: "catClient", FieldName: "Cat", Parent: "Client"},
	{TypeName: "clusterClient", FieldName: "Cluster", Parent: "Client"},
	{TypeName: "danglingClient", FieldName: "Dangling", Parent: "Client"},
	{TypeName: "documentClient", FieldName: "Doc", Parent: "Client", Aliases: []string{"Document"}},
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
	{TypeName: "pointInTimeClient", FieldName: "PIT", Parent: "Client", Aliases: []string{"PointInTime"}},
	{TypeName: "ingestClient", FieldName: "Ingest", Parent: "Client"},
	{TypeName: "tasksClient", FieldName: "Tasks", Parent: "Client"},
	{TypeName: "scrollClient", FieldName: "Scroll", Parent: "Client"},
	{TypeName: "searchPipelineClient", FieldName: "SearchPipeline", Parent: "Client"},
	{TypeName: "snapshotClient", FieldName: "Snapshot", Parent: "Client"},
	{TypeName: "repositoryClient", FieldName: "Repository", Parent: "snapshotClient"},
}

// resolveFieldPath translates a sub-client receiver type name into the
// dotted field path used to access it from a *Client. e.g.
// "clusterClient" -> "Cluster", "aliasClient" -> "Indices.Alias".
func resolveFieldPath(receiverType string) string {
	for _, sc := range subClientHierarchy {
		if sc.TypeName == receiverType {
			if sc.Parent == "Client" {
				return sc.FieldName
			}
			return resolveFieldPath(sc.Parent) + "." + sc.FieldName
		}
	}
	return receiverType
}

// parentOf returns the parent client type for a sub-client type name, or "" if
// the type is not in the hierarchy.
func parentOf(typeName string) string {
	for _, sc := range subClientHierarchy {
		if sc.TypeName == typeName {
			return sc.Parent
		}
	}
	return ""
}

// usedSubClientTypes returns the set of sub-client type names that at least one
// operation routes to. A type is "used" when any dispatch route targets it
// (deprecated forwarding routes count, so nested aliases like aliasClient stay).
// The set is expanded to include every ancestor so a retained child never
// references a dropped parent.
func usedSubClientTypes(ops []*ir.Operation) map[string]bool {
	used := make(map[string]bool)
	for _, op := range ops {
		for _, dr := range op.DispatchRoutes {
			if dr.TopLevel || dr.ReceiverType == "" || dr.ReceiverType == "Client" {
				continue
			}
			used[dr.ReceiverType] = true
		}
	}
	// Snapshot the seed types before walking parents so we don't mutate the map
	// mid-range.
	seeds := make([]string, 0, len(used))
	for t := range used {
		seeds = append(seeds, t)
	}
	for _, t := range seeds {
		for p := parentOf(t); p != "" && p != "Client"; p = parentOf(p) {
			used[p] = true
		}
	}
	return used
}

// filterSubClients returns the subClientHierarchy entries whose type is in used,
// preserving the original order (so parents still precede their children).
func filterSubClients(used map[string]bool) []subClientInfo {
	out := make([]subClientInfo, 0, len(subClientHierarchy))
	for _, sc := range subClientHierarchy {
		if used[sc.TypeName] {
			out = append(out, sc)
		}
	}
	return out
}

// prefixToReceiverType maps a group prefix to its primary (flat) receiver
// type. Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var prefixToReceiverType = map[string]string{
	"cat":              "catClient",
	"cluster":          "clusterClient",
	"dangling_indices": "danglingClient",
	"indices":          "indicesClient",
	"ingest":           "ingestClient",
	"nodes":            "nodesClient",
	"remote_store":     "Client",
	"scroll":           "scrollClient",
	"search_pipeline":  "searchPipelineClient",
	"snapshot":         "snapshotClient",
	"tasks":            "tasksClient",
}

// unprefixedGroupOverrides routes prefix-less operation groups (no dot) that
// would otherwise resolve to top-level Client methods, to a specific sub-client
// to avoid field/method name collisions or maintain backward compatibility.
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var unprefixedGroupOverrides = map[string]dispatchRoute{
	"clear_scroll": {ReceiverType: "scrollClient", MethodName: "Delete", TopLevel: false},
	"scroll":       {ReceiverType: "scrollClient", MethodName: "Get", TopLevel: false},
}

// unprefixedSubClientGroup assigns a set of prefix-less operation groups to a
// single sub-client receiver. The OpenAPI spec leaves these groups prefix-less
// (e.g. "create", "get", "create_pit"), so they would otherwise resolve to
// top-level Client methods; this table routes them onto a sub-client instead,
// mirroring how the OpenSearch server groups them by REST-action package
// (document operations under one family, point-in-time under another).
type unprefixedSubClientGroup struct {
	ReceiverType string   // sub-client receiver type (e.g. "documentClient")
	TrimSuffixes []string // tail tokens stripped before deriving the method name, longest-first
	Groups       []string // prefix-less group names owned by this sub-client
}

// unprefixedSubClientGroups drives the prefix-less sub-client assignments folded
// into unprefixedGroupOverrides by init(). Keep ReceiverTypes that also appear in
// subClientHierarchy.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var unprefixedSubClientGroups = []unprefixedSubClientGroup{
	{
		ReceiverType: "pointInTimeClient",
		// "create_pit" -> Create, "get_all_pits" -> GetAll: drop the redundant
		// pit/pits tail since the sub-client name already conveys it.
		TrimSuffixes: []string{"_pits", "_pit"},
		Groups:       []string{"create_pit", "delete_pit", "get_all_pits", "delete_all_pits"},
	},
	{
		ReceiverType: "documentClient",
		// The 13 operations the OpenSearch server groups under its
		// rest/action/document/ package. Group names are already verb-only, so
		// no suffix trimming is needed ("create" -> Create, "bulk" -> Bulk).
		Groups: []string{
			"index", "create", "get", "get_source", "exists", "exists_source",
			"delete", "update", "mget", "bulk", "bulk_stream",
			"termvectors", "mtermvectors",
		},
	},
}

// trimSuffixes removes the first matching suffix in suffixes from s (which is
// scanned longest-first by the caller's table ordering). It returns s unchanged
// when nothing matches.
func trimSuffixes(s string, suffixes []string) string {
	for _, suf := range suffixes {
		if trimmed, ok := strings.CutSuffix(s, suf); ok {
			return trimmed
		}
	}
	return s
}

//nolint:gochecknoinits // folds the declarative sub-client table into the override map once at startup
func init() {
	for _, sc := range unprefixedSubClientGroups {
		for _, group := range sc.Groups {
			unprefixedGroupOverrides[group] = dispatchRoute{
				ReceiverType: sc.ReceiverType,
				MethodName:   methodNameFromSuffix(trimSuffixes(group, sc.TrimSuffixes)),
				TopLevel:     false,
			}
		}
	}
}

// resolveDispatchRoutes returns all dispatch routes for an operation group.
// The first route is always the canonical flat method on the prefix's client.
// If the operation has a nested sub-client override, a second deprecated
// forwarding route is appended. Compatibility forwarder routes (see
// compatForwarders) are appended last so callers can keep or drop them based on
// the --emit-v4-compat flag.
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

	routes = append(routes, compatForwardersFor(group, primary)...)

	return routes
}

// compatForwarder describes a backward-compatibility method that forwards to a
// canonical method elsewhere. It exists so code written against the historical
// API surface keeps compiling once operations move onto sub-clients.
type compatForwarder struct {
	ReceiverType string // receiver the compat method is declared on
	MethodName   string // the historical method name
}

// compatForwarders maps an operation group to the compatibility methods that
// should forward to its canonical (primary) route. Two shapes appear:
//
//   - top-level forwarders: operations that were reachable as bare
//     client.Bulk/MGet/Update now live on documentClient; the forwarder
//     restores the top-level Client method. The index document op is
//     deliberately excluded: Index is now the indices sub-client field on
//     Client (see indicesClient.FieldName), and a field and a method of the
//     same name cannot coexist in Go, so client.Doc.Index is the only
//     spelling -- there is no top-level client.Index(...) forwarder.
//   - same-receiver name aliases: an operation whose canonical method name was
//     renamed keeps its historical name as a forwarder on the same sub-client
//     (e.g. documentClient.Source -> GetSource, pointInTimeClient.Get -> GetAll).
//
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var compatForwarders = map[string][]compatForwarder{
	"bulk":         {{ReceiverType: "Client", MethodName: "Bulk"}},
	"get_all_pits": {{ReceiverType: "pointInTimeClient", MethodName: "Get"}},
	"get_source":   {{ReceiverType: "documentClient", MethodName: "Source"}},
	"index":        {{ReceiverType: "Client", MethodName: "Index"}},
	"mget":         {{ReceiverType: "Client", MethodName: "MGet"}},
	"update":       {{ReceiverType: "Client", MethodName: "Update"}},
}

// compatForwardersFor builds the dispatchRoutes for a group's compatibility
// forwarders, each pointing at the canonical primary route. The Forward
// expression is relative to the forwarder's own receiver: a top-level forwarder
// reaches the sub-client via its field path (e.g. "Doc.Bulk"), while a
// same-receiver alias names the canonical method directly (e.g. "GetAll").
func compatForwardersFor(group string, primary dispatchRoute) []dispatchRoute {
	cfs, ok := compatForwarders[group]
	if !ok {
		return nil
	}
	out := make([]dispatchRoute, 0, len(cfs))
	for _, cf := range cfs {
		forward := primary.MethodName
		if cf.ReceiverType == "Client" && !primary.TopLevel {
			forward = resolveFieldPath(primary.ReceiverType) + "." + primary.MethodName
		}
		out = append(out, dispatchRoute{
			ReceiverType: cf.ReceiverType,
			MethodName:   cf.MethodName,
			TopLevel:     cf.ReceiverType == "Client",
			Forward:      forward,
		})
	}
	return out
}

// CompatConfig controls which backward-compatibility forwarders the generator
// emits. Members are version-scoped (V4*) so a future v5/v6 compatibility layer
// can be added without restructuring callers.
type CompatConfig struct {
	// V4Compat emits the v4 compatibility forwarder methods (e.g. top-level
	// Client.Bulk forwarding to Doc.Bulk, documentClient.Source forwarding to
	// GetSource).
	V4Compat bool
	// V4Deprecation marks those forwarders with a Deprecated doc comment. It has
	// no effect unless V4Compat is set.
	V4Deprecation bool
}

// applyCompatPolicy rewrites each operation's dispatch routes in place to honor
// the compatibility config: forwarder routes (those with a non-empty Forward)
// are dropped when V4Compat is false, and marked Deprecated when V4Deprecation
// is set. Non-forwarder routes are left untouched.
func applyCompatPolicy(ops []*ir.Operation, compat CompatConfig) {
	for _, op := range ops {
		kept := op.DispatchRoutes[:0]
		for _, dr := range op.DispatchRoutes {
			if dr.Forward != "" {
				if !compat.V4Compat {
					continue
				}
				if compat.V4Deprecation {
					dr.Deprecated = true
				}
			}
			kept = append(kept, dr)
		}
		op.DispatchRoutes = kept
	}
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
	if _, after, ok := strings.Cut(group, "."); ok {
		suffix = after
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
	return applyIdiomaticAbbreviations(sb.String())
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
		if _, after, ok := strings.Cut(g, "."); ok {
			suffix = after
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
// longest-first so longer prefixes match before shorter ones (so the
// order is significant; alphabetizing would be wrong).
//
//nolint:gochecknoglobals // const-ish read-only lookup table
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
// Keys are sorted alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var irregularSingulars = map[string]string{
	"caches":  "cache",
	"indices": "index",
	"niches":  "niche",
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
