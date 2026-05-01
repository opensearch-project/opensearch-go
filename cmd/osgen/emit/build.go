// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// BuildConfig holds configuration for target construction.
type BuildConfig struct {
	OutDir     string
	PluginsDir string
	CorePkg    string
	ModulePath string
	SubClients []SubClient
}

// Build constructs all targets from the IR spec.
func Build(spec *ir.Spec, cfg BuildConfig) []Target {
	var targets []Target

	type pluginInfo struct {
		dir         string
		pkg         string
		hasDuration bool
		ops         []*ir.Operation
	}
	pluginPkgs := make(map[string]*pluginInfo)

	for _, op := range spec.Operations {
		pkg, dir := routeOp(op.Group, cfg.OutDir, cfg.PluginsDir)
		filePkg := pkg
		if filePkg == "opensearchapi" {
			filePkg = cfg.CorePkg
		}
		basename := opFilename(op.Group)
		importPath := importPathForGroup(op.Group, cfg.CorePkg, cfg.ModulePath)

		// Operation file (req + params + resp + dispatch + siblings).
		targets = append(targets, buildOperationFile(dir, filePkg, basename, op, spec.Registry))

		// Params test file.
		if paramsFrag := buildParamsTestFrag(op, filePkg); paramsFrag != nil {
			targets = append(targets, NewParamsTestFile(dir, filePkg, basename, paramsFrag))
		}

		// Req test file.
		if reqFrag := buildReqTestFrag(op, filePkg, importPath); reqFrag != nil {
			targets = append(targets, NewReqTestFile(dir, filePkg, basename, reqFrag))
		}

		// Integration test file.
		if integFrag := buildIntegTestFrag(op, filePkg, importPath, cfg); integFrag != nil {
			targets = append(targets, NewIntegTestFile(dir, filePkg, basename, integFrag))
		}

		// Track plugin packages.
		if op.IsPlugin {
			pi, ok := pluginPkgs[pkg]
			if !ok {
				pi = &pluginInfo{dir: dir, pkg: pkg}
				pluginPkgs[pkg] = pi
			}
			pi.ops = append(pi.ops, op)
			if !pi.hasDuration {
				for _, p := range op.QueryParams {
					if p.Kind == ir.ParamDuration {
						pi.hasDuration = true
						break
					}
				}
			}
		}
	}

	// Clients file (core package).
	if t := NewClientsFile(cfg.OutDir, cfg.CorePkg, cfg.SubClients); t != nil {
		targets = append(targets, t)
	}

	// Shared types and union types (core package).
	if t := NewSharedTypesFile(cfg.OutDir, cfg.CorePkg, spec.Types); t != nil {
		targets = append(targets, t)
	}
	if t := NewUnionTypesFile(cfg.OutDir, cfg.CorePkg, spec.Types); t != nil {
		targets = append(targets, t)
	}

	// Core compat file is NOT generated - the core package has hand-written
	// Inspect alias, noBody sentinel, and formatDuration in api.go/clients_gen.go.

	// Dispatch test file (core operations only).
	if frag := buildDispatchTestFrag(spec.Operations, cfg.CorePkg, cfg.ModulePath+"/"+cfg.CorePkg); frag != nil {
		targets = append(targets, NewDispatchTestFile(cfg.OutDir, cfg.CorePkg, frag))
	}

	// Plugin packages (sorted for deterministic output).
	pluginNames := make([]string, 0, len(pluginPkgs))
	for pkg := range pluginPkgs {
		pluginNames = append(pluginNames, pkg)
	}
	sort.Strings(pluginNames)

	for _, pkg := range pluginNames {
		pi := pluginPkgs[pkg]
		// Compat file.
		if t := NewCompatFile(pi.dir, pkg, pi.hasDuration); t != nil {
			targets = append(targets, t)
		}

		// Plugin client file.
		if t := NewPluginClientFile(pi.dir, pkg, pi.ops); t != nil {
			targets = append(targets, t)
		}

		// Plugin test helper file (skip if hand-written helpers already exist).
		pluginImport := importPathForGroup(pi.ops[0].Group, cfg.CorePkg, cfg.ModulePath)
		coreImport := cfg.ModulePath + "/" + cfg.CorePkg
		testDir := pi.dir + "/internal/test"
		if !hasExistingHelper(testDir) {
			targets = append(targets, NewPluginTestHelperFile(testDir, pkg, pluginImport, coreImport, cfg.CorePkg))
		}
	}

	return targets
}

func buildOperationFile(dir, pkg, basename string, op *ir.Operation, reg *ir.TypeRegistry) Target {
	var frags []Fragment

	frags = append(frags, &ReqFragment{Op: op})
	frags = append(frags, &ParamsFragment{Op: op})
	if op.Response != nil && !op.IsNoBody {
		frags = append(frags, &RespFragment{Op: op, Registry: reg})
	}
	if len(op.SiblingTypes) > 0 {
		var structSiblings, unionSiblings []*ir.Type
		for _, st := range op.SiblingTypes {
			if st.Kind == ir.TypeUnion || st.Kind == ir.TypeLazyUnion {
				unionSiblings = append(unionSiblings, st)
			} else {
				structSiblings = append(structSiblings, st)
			}
		}
		if len(structSiblings) > 0 {
			frags = append(frags, &SiblingTypesFragment{Op: op, Types: structSiblings, Registry: reg})
		}
		if len(unionSiblings) > 0 {
			frags = append(frags, &UnionFragment{Types: unionSiblings})
		}
	}
	if len(op.DispatchRoutes) > 0 {
		frags = append(frags, &DispatchFragment{Op: op})
	}

	return &File{
		FilePath:  dir + "/" + basename + "_gen.go",
		Package:   pkg,
		Fragments: frags,
	}
}

func buildParamsTestFrag(op *ir.Operation, pkg string) *ParamsTestFragment {
	if len(op.QueryParams) == 0 {
		return nil
	}

	hasDuration := false
	var cases []ParamTestCase
	for _, p := range op.QueryParams {
		if p.Kind == ir.ParamDuration {
			hasDuration = true
		}
		tc := ParamTestCase{Name: p.WireName}
		tc.FieldAssign, tc.WantAssign = paramTestValues(p)
		cases = append(cases, tc)
	}

	return &ParamsTestFragment{
		TypePrefix:  op.TypePrefix,
		HasDuration: hasDuration,
		Cases:       cases,
	}
}

func paramTestValues(p ir.QueryParam) (fieldAssign, wantAssign string) {
	switch p.Kind {
	case ir.ParamDuration:
		fieldAssign = fmt.Sprintf("%s: 5 * time.Second", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "5000ms")
	case ir.ParamBool:
		fieldAssign = fmt.Sprintf("%s: true", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "true")
	case ir.ParamList:
		fieldAssign = fmt.Sprintf(`%s: []string{"a", "b"}`, p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "a,b")
	case ir.ParamInt:
		fieldAssign = fmt.Sprintf("%s: 42", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "42")
	default:
		fieldAssign = fmt.Sprintf("%s: %q", p.GoName, "test-value")
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "test-value")
	}
	return fieldAssign, wantAssign
}

func buildReqTestFrag(op *ir.Operation, pkg, importPath string) *ReqTestFragment {
	cases := synthesizeReqCasesIR(op)
	if len(cases) == 0 {
		return nil
	}

	needsStrings := false
	for _, c := range cases {
		if strings.Contains(c.FieldAssign, "strings.NewReader") {
			needsStrings = true
			break
		}
	}

	return &ReqTestFragment{
		PkgName:      pkg,
		ImportPath:   importPath,
		TypePrefix:   op.TypePrefix,
		NeedsStrings: needsStrings,
		Cases:        cases,
	}
}

func synthesizeReqCasesIR(op *ir.Operation) []ReqTestCase {
	var cases []ReqTestCase

	hasRequiredPath := false
	for _, f := range op.PathFields {
		if f.Required {
			hasRequiredPath = true
			break
		}
	}

	if !hasRequiredPath {
		basePath := evalPathBuilder(op.PathBuilder, nil)
		cases = append(cases, ReqTestCase{
			Name:       "empty request",
			WantMethod: PrimaryMethod(op),
			WantPath:   basePath,
			WantErr:    "false",
		})
	} else {
		cases = append(cases, ReqTestCase{
			Name:    "missing required fields",
			WantErr: "true",
		})
	}

	if len(op.PathFields) > 0 {
		var assigns []string
		substitutions := make(map[string]string)
		for _, f := range op.PathFields {
			if f.IsList {
				assigns = append(assigns, fmt.Sprintf(`%s: []string{"a", "b"}`, f.GoName))
				substitutions[f.GoName] = "a,b"
			} else {
				testVal := "test-" + strings.ToLower(f.GoName)
				assigns = append(assigns, fmt.Sprintf(`%s: %q`, f.GoName, testVal))
				substitutions[f.GoName] = testVal
			}
		}
		wantMethod, fullPath := PrimaryMethod(op), evalPathBuilder(op.PathBuilder, substitutions)
		cases = append(cases, ReqTestCase{
			Name:        "all path fields",
			FieldAssign: strings.Join(assigns, ", "),
			WantMethod:  wantMethod,
			WantPath:    fullPath,
			WantErr:     "false",
		})
	}

	if switchMethod := bodyMethodSwitch(op); switchMethod != "" {
		var assigns []string
		substitutions := make(map[string]string)
		for _, f := range op.PathFields {
			if f.IsList {
				assigns = append(assigns, fmt.Sprintf(`%s: []string{"x"}`, f.GoName))
				substitutions[f.GoName] = "x"
			} else {
				assigns = append(assigns, fmt.Sprintf(`%s: %q`, f.GoName, "test"))
				substitutions[f.GoName] = "test"
			}
		}
		assigns = append(assigns, `Body: strings.NewReader("{}")`)
		wantPath := evalPathBuilder(op.PathBuilder, substitutions)
		cases = append(cases, ReqTestCase{
			Name:        "body triggers " + switchMethod,
			FieldAssign: strings.Join(assigns, ", "),
			WantMethod:  switchMethod,
			WantPath:    wantPath,
			WantErr:     "false",
		})
	}

	return cases
}

// evalPathBuilder simulates the path builder ops with the given field values to
// produce the expected (method, path). This handles conditional segments and
// method selection correctly.
func evalPathBuilder(pb ir.PathBuilder, values map[string]string) string {
	var segments []string
	skip := false
	taken := false
	depth := 0

	for _, op := range pb.Ops {
		switch op.Kind {
		case ir.PathOpLit:
			if !skip {
				segments = append(segments, op.Value)
			}
		case ir.PathOpField:
			if !skip {
				if v, ok := values[op.Value]; ok {
					segments = append(segments, v)
				}
			}
		case ir.PathOpList:
			if !skip {
				if v, ok := values[op.Value]; ok {
					segments = append(segments, v)
				}
			}
		case ir.PathOpIfStr, ir.PathOpIfList:
			depth++
			if _, ok := values[op.Value]; ok {
				skip = false
				taken = true
			} else {
				skip = true
				taken = false
			}
		case ir.PathOpElseIfStr, ir.PathOpElseIfList:
			if depth == 1 {
				if taken {
					skip = true
				} else if _, ok := values[op.Value]; ok {
					skip = false
					taken = true
				} else {
					skip = true
				}
			}
		case ir.PathOpElse:
			if depth == 1 {
				if taken {
					skip = true
				} else {
					skip = false
					taken = true
				}
			}
		case ir.PathOpEnd:
			depth--
			if depth == 0 {
				skip = false
				taken = false
			}
		}
	}

	path := "/" + strings.Join(segments, "/")
	if len(segments) == 0 {
		path = "/"
	}
	return path
}

func buildDispatchTestFrag(ops []*ir.Operation, corePkg, coreImport string) *DispatchTestFragment {
	var entries []DispatchEntry
	for _, op := range ops {
		if op.IsPlugin {
			continue
		}
		for _, route := range op.DispatchRoutes {
			if route.Deprecated {
				continue
			}
			entry := DispatchEntry{
				TestName:   op.TypePrefix,
				MethodName: route.MethodName,
				FieldPath:  route.FieldPath,
			}

			if op.IsPointerReq {
				entry.ReqType = fmt.Sprintf("*%s.%sReq", corePkg, op.TypePrefix)
			} else {
				entry.ReqType = fmt.Sprintf("%s.%sReq", corePkg, op.TypePrefix)
			}

			if op.IsNoBody {
				entry.RespType = "*opensearch.Response"
			} else {
				entry.RespType = fmt.Sprintf("*%s.%sResp", corePkg, op.TypePrefix)
			}

			entries = append(entries, entry)
		}
	}

	if len(entries) == 0 {
		return nil
	}

	return &DispatchTestFragment{
		PkgName:    corePkg,
		ImportPath: coreImport,
		Entries:    entries,
	}
}

func buildIntegTestFrag(op *ir.Operation, pkg, importPath string, cfg BuildConfig) *IntegTestFragment {
	isPlugin := op.IsPlugin
	config := classifyOpIR(op, pkg, cfg.CorePkg, isPlugin, cfg.SubClients)

	return &IntegTestFragment{
		PkgName:    pkg,
		ImportPath: importPath,
		ModulePath: cfg.ModulePath,
		CorePkg:    cfg.CorePkg,
		Config:     config,
	}
}

func classifyOpIR(op *ir.Operation, pkg, corePkg string, isPlugin bool, subClients []SubClient) IntegTestConfig {
	cfg := IntegTestConfig{
		TypePrefix:  op.TypePrefix,
		IsNoBody:    op.IsNoBody,
		IsPlugin:    isPlugin,
		CorePkgName: corePkg,
	}

	versionAdded := op.VersionAdded
	if versionAdded == "1.0.0" || versionAdded == "1.0" {
		versionAdded = ""
	}
	cfg.VersionAdded = versionAdded

	var callPrefix string
	if isPlugin {
		suffix := op.Group
		if idx := strings.IndexByte(suffix, '.'); idx >= 0 {
			suffix = suffix[idx+1:]
		}
		callPrefix = "client." + PluginMethodName(suffix)
	} else {
		route := primaryRouteIR(op)
		callPrefix = "client."
		if route.FieldPath != "" {
			callPrefix += route.FieldPath + "."
		}
		callPrefix += route.MethodName
	}

	hasRequiredIndex := false
	hasRequiredID := false
	hasOtherRequired := false
	for _, f := range op.PathBuilder.Fields {
		if !f.Required {
			continue
		}
		switch f.Name {
		case "Index":
			hasRequiredIndex = true
		case "ID", "DocumentID":
			hasRequiredID = true
		default:
			hasOtherRequired = true
		}
	}

	primary := PrimaryMethod(op)
	isMutating := primary == http.MethodPost ||
		primary == http.MethodPut ||
		primary == http.MethodDelete ||
		primary == http.MethodPatch

	needsBody := op.HasBody && primary != http.MethodGet

	if skipReason := integSkipReason(op.Group); skipReason != "" {
		cfg.SkipReason = skipReason
	}

	cfg.ResourcePrefix = "test-" + kebabCaseIR(op.TypePrefix)

	// Determine fixture based on operation semantics.
	fixtureKind := integFixtureKind(op.Group, hasRequiredIndex, hasRequiredID)
	switch fixtureKind {
	case fixtureDoc:
		cfg.FixtureCode = buildDocFixtureIR(corePkg, isPlugin)
	case fixtureIndex:
		cfg.FixtureCode = buildIndexFixtureIR(corePkg, isPlugin)
	case fixtureIndexOnly:
		cfg.FixtureCode = "// index variable defined above; the operation itself creates the resource."
	case fixtureComponentTemplate:
		cfg.FixtureCode = buildComponentTemplateFixtureIR(corePkg, isPlugin)
	case fixtureIndexTemplate:
		cfg.FixtureCode = buildIndexTemplateFixtureIR(corePkg, isPlugin)
	case fixtureLegacyTemplate:
		cfg.FixtureCode = buildLegacyTemplateFixtureIR(corePkg, isPlugin)
	case fixtureAlias:
		cfg.FixtureCode = buildAliasFixtureIR(corePkg, isPlugin)
	case fixtureWriteAlias:
		cfg.FixtureCode = buildWriteAliasFixtureIR(corePkg, isPlugin)
	case fixturePipeline:
		cfg.FixtureCode = buildPipelineFixtureIR(corePkg, isPlugin)
	case fixtureScript:
		cfg.FixtureCode = buildScriptFixtureIR(corePkg, isPlugin)
	case fixtureDataStream:
		cfg.FixtureCode = buildDataStreamFixtureIR(corePkg, isPlugin)
	}

	// Determine which unique-string variables the test needs, based on
	// operation structure and fixture kind.
	switch fixtureKind {
	case fixtureNone:
		cfg.NeedsIndex = hasRequiredIndex
	default:
		cfg.NeedsIndex = true
	}
	cfg.NeedsDocID = hasRequiredID || fixtureUsesDocID(fixtureKind)

	bodyOverride := integBodyOverride(op.Group)

	// Only count optional name fields when the call expression will actually
	// build a non-nil request literal (otherwise the variable is unused).
	willBuildReq := hasRequiredIndex || hasRequiredID || hasOtherRequired || needsBody || bodyOverride != ""
	if !op.IsPointerReq {
		willBuildReq = true
	}
	cfg.NeedsName = hasOtherRequired || fixtureNeedsName(fixtureKind) || hasRequiredStringParam(op) || (willBuildReq && hasOptionalNameField(op))

	cfg.CallExpr = buildCallExprIR(callPrefix, op, pkg, hasRequiredIndex, hasRequiredID, hasOtherRequired, needsBody, isMutating, bodyOverride, false)
	cfg.FailCallExpr = buildCallExprIR(callPrefix, op, pkg, hasRequiredIndex, hasRequiredID, hasOtherRequired, needsBody, isMutating, bodyOverride, true)

	// Build all necessary query params (required params + cat format=json).
	if paramStr := buildIntegParams(op, pkg); paramStr != "" {
		cfg.CallExpr = addParamField(cfg.CallExpr, op, pkg, paramStr)
	}

	return cfg
}

func primaryRouteIR(op *ir.Operation) ir.DispatchRoute {
	for _, route := range op.DispatchRoutes {
		if !route.Deprecated {
			return route
		}
	}
	return ir.DispatchRoute{MethodName: op.TypePrefix}
}

func buildCallExprIR(callPrefix string, op *ir.Operation, pkg string, hasRequiredIndex, hasRequiredID, hasOtherRequired, needsBody, isMutating bool, bodyOverride string, isFailing bool) string {
	prefix := callPrefix
	if isFailing {
		prefix = strings.Replace(callPrefix, "client.", "failingClient.", 1)
	}

	if op.IsPointerReq {
		if !hasRequiredIndex && !hasRequiredID && !hasOtherRequired && !needsBody && bodyOverride == "" {
			return prefix + "(t.Context(), nil)"
		}
		return prefix + "(t.Context(), &" + buildReqLiteralIR(op, pkg, hasRequiredIndex, hasRequiredID, needsBody, isMutating, bodyOverride) + ")"
	}
	return prefix + "(t.Context(), " + buildReqLiteralIR(op, pkg, hasRequiredIndex, hasRequiredID, needsBody, isMutating, bodyOverride) + ")"
}

func buildReqLiteralIR(op *ir.Operation, pkg string, hasRequiredIndex, hasRequiredID, needsBody, isMutating bool, bodyOverride string) string {
	var fields []string

	// Populate all path fields in the test request for a comprehensive happy-path
	// exercise. Optional fields are included too, since they exercise longer URL
	// variants.
	forceIndex := op.Group == "indices.put_alias" || op.Group == "indices.put_settings"

	for _, f := range op.PathBuilder.Fields {
		if !f.Required {
			switch f.Name {
			case "Index":
				if forceIndex {
					if f.IsList {
						fields = append(fields, "Index: []string{index}")
					} else {
						fields = append(fields, "Index: index")
					}
				}
			case "ID", "DocumentID", "NewIndex", "Context":
				// optional ID, NewIndex, and Context fields are not populated
			default:
				if f.IsList {
					fields = append(fields, fmt.Sprintf("%s: []string{name}", f.Name))
				} else {
					fields = append(fields, fmt.Sprintf("%s: name", f.Name))
				}
			}
			continue
		}
		switch f.Name {
		case "Index":
			if f.IsList {
				fields = append(fields, "Index: []string{index}")
			} else {
				fields = append(fields, "Index: index")
			}
		case "ID", "DocumentID":
			fields = append(fields, f.Name+": docID")
		default:
			if f.IsList {
				fields = append(fields, fmt.Sprintf("%s: []string{name}", f.Name))
			} else {
				fields = append(fields, fmt.Sprintf("%s: name", f.Name))
			}
		}
	}

	if bodyOverride != "" {
		fields = append(fields, "Body: "+bodyOverride)
	} else if needsBody {
		fields = append(fields, `Body: strings.NewReader("{}")`)
	}

	if isMutating && hasRequiredIndex && !hasRequiredID && hasRefreshParamIR(op) {
		fields = append(fields, fmt.Sprintf("Params: %s.%sParams{Refresh: %q}", pkg, op.TypePrefix, "true"))
	}

	return fmt.Sprintf("%s.%sReq{%s}", pkg, op.TypePrefix, strings.Join(fields, ", "))
}

func hasRefreshParamIR(op *ir.Operation) bool {
	for _, p := range op.QueryParams {
		if p.GoName == "Refresh" {
			return true
		}
	}
	return false
}

func buildIndexFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.Create(t.Context(), %s.IndicesCreateReq{Index: index})
	require.NoError(t, err)`, c, corePkg)
}

func buildDocFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.Create(t.Context(), %s.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	_, err = %s.Index(t.Context(), %s.IndexReq{
		Index:  index,
		ID:     docID,
		Body:   strings.NewReader(`+"`"+`{"title":"fixture"}`+"`"+`),
		Params: %s.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)`, c, corePkg, c, corePkg, corePkg)
}

func integSkipReason(group string) string {
	switch {
	case strings.HasPrefix(group, "dangling_indices"):
		return "requires dangling index state from node failure"
	case strings.HasPrefix(group, "snapshot"):
		return "requires snapshot repository configuration"
	case group == "reindex":
		return "requires source index with documents"
	case group == "reindex_rethrottle":
		return "requires active reindex task"
	case strings.HasPrefix(group, "tasks"):
		return "requires active long-running task"
	case group == "bulk" || group == "bulk_stream":
		return "requires NDJSON body with action metadata"
	case group == "msearch" || group == "msearch_template":
		return "requires NDJSON multi-search body"
	case group == "scroll.get" || group == "scroll.delete":
		return "requires active scroll context"
	case group == "clear_scroll":
		return "requires active scroll context"
	case strings.HasPrefix(group, "pit."):
		return "requires point-in-time context"
	case group == "rank_eval":
		return "requires rank evaluation body with rated documents"
	case group == "scripts_painless.execute" || group == "scripts_painless_execute":
		return "requires painless script body"
	case group == "render_search_template":
		return "requires search template body"
	case group == "termvectors" || group == "mtermvectors":
		return "requires index with term vectors enabled"
	case group == "indices.clone":
		return "requires read-only source index"
	case group == "indices.split":
		return "requires source index with number_of_routing_shards > number_of_shards"
	case group == "indices.shrink":
		return "requires source index on single node with read-only"
	case group == "cluster.allocation_explain":
		return "requires unassigned shards"
	case group == "cluster.post_voting_config_exclusions" ||
		group == "cluster.delete_voting_config_exclusions":
		return "requires multi-node cluster with voting configuration"
	case group == "cluster.put_decommission_awareness" ||
		group == "cluster.delete_decommission_awareness" ||
		group == "cluster.get_decommission_awareness":
		return "requires awareness attributes configured"
	case group == "cluster.put_weighted_routing" ||
		group == "cluster.get_weighted_routing" ||
		group == "cluster.delete_weighted_routing":
		return "requires awareness attributes and weighted routing setup"
	case group == "remote_store.restore":
		return "requires remote store configuration"
	case group == "cluster.stats":
		return "path builder emits /nodes segment unconditionally"
	case group == "_core.create" || group == "create":
		return "op_type=create conflicts with doc fixture"
	case group == "indices.add_block":
		return "requires valid block name (write/read/read_only/metadata)"
	case group == "indices.create_data_stream":
		return "requires matching index template with data_stream"
	case group == "indices.delete_data_stream":
		return "requires existing data stream"
	case group == "scroll":
		return "requires active scroll context"
	case group == "delete_by_query_rethrottle" ||
		group == "update_by_query_rethrottle":
		return "requires active long-running query task"
	case group == "delete_pit":
		return "requires active point-in-time context"
	case group == "nodes.hot_threads":
		return "returns plain text, not JSON"
	case group == "cat.all_pit_segments" || group == "cat.pit_segments":
		return "requires active point-in-time context"
	case group == "cat.segment_replication":
		return "requires segment replication enabled"
	case group == "cat.help":
		return "returns plain text, not JSON"
	case group == "search_pipeline.get":
		return "requires search pipeline to exist"
	case group == "search_pipeline.delete":
		return "requires search pipeline to exist"
	case group == "cat.snapshots":
		return "requires snapshot repository configuration"
	case group == "list.help":
		return "returns plain text, not JSON"
	case strings.HasPrefix(group, "list."):
		return "response struct does not match cat-style response format"

	// Async search operations require a valid async search ID from a prior submit.
	case group == "asynchronous_search.delete" ||
		group == "asynchronous_search.get":
		return "requires async search ID from submit"
	case group == "asynchronous_search.stats" ||
		group == "asynchronous_search.search":
		return "response struct does not match actual response format"

	// Flow framework operations require workflow IDs from prior create.
	case strings.HasPrefix(group, "flow_framework"):
		return "requires workflow ID from prior create"

	// Geospatial operations require external network access or datasource setup.
	case strings.HasPrefix(group, "geospatial"):
		return "requires IP2Geo datasource or external network access"

	// Insights top_queries requires valid metric type.
	case group == "insights.top_queries":
		return "requires valid metric type (cpu, memory, latency)"

	// ISM operations require existing policies or policy-attached indices.
	case group == "ism.add_policy" || group == "ism.change_policy":
		return "requires policy_id in request body"
	case group == "ism.remove_policy" || group == "ism.retry_index":
		return "requires index with ISM policy attached"
	case group == "ism.put_policy" || group == "ism.put_policies":
		return "requires valid ISM policy document body"
	case group == "ism.delete_policy" || group == "ism.exists_policy" || group == "ism.get_policy":
		return "requires existing ISM policy"
	case group == "ism.explain_policy" || group == "ism.refresh_search_analyzers":
		return "response struct does not match actual response format"
	case group == "ism.get_policies":
		return "response struct does not match actual response format"

	// KNN operations require model training infrastructure.
	case strings.HasPrefix(group, "knn"):
		return "requires KNN model training data and infrastructure"

	// LTR operations require Learning to Rank store setup.
	case strings.HasPrefix(group, "ltr"):
		return "requires LTR feature store"

	// ML operations require model/connector registration and deployment.
	case strings.HasPrefix(group, "ml"):
		return "requires ML model or connector registration"

	// Neural operations require neural search model deployment.
	case strings.HasPrefix(group, "neural"):
		return "requires deployed neural search model"

	// Notifications operations require config objects.
	case strings.HasPrefix(group, "notifications"):
		return "requires notification channel or config"

	// Observability operations require saved objects.
	case strings.HasPrefix(group, "observability"):
		return "requires observability saved object"

	// PPL/SQL operations require valid query bodies.
	case strings.HasPrefix(group, "ppl"):
		return "requires valid PPL query body"
	case strings.HasPrefix(group, "sql"):
		return "requires valid SQL query body"
	case strings.HasPrefix(group, "query"):
		return "requires valid query DSL body"

	// Replication requires cross-cluster setup.
	case strings.HasPrefix(group, "replication"):
		return "requires cross-cluster replication setup"

	// Rollups require rollup job definition.
	case strings.HasPrefix(group, "rollups"):
		return "requires rollup job configuration"

	// Security plugin operations require security resources.
	case strings.HasPrefix(group, "security"):
		return "requires security plugin resources"

	// Snapshot management requires SM policy.
	case strings.HasPrefix(group, "sm"):
		return "requires snapshot management policy"

	// Transforms require transform job.
	case strings.HasPrefix(group, "transforms"):
		return "requires transform job configuration"

	// WLM operations require workload group setup.
	case strings.HasPrefix(group, "wlm"):
		return "requires workload management query group"
	}
	return ""
}

type fixtureType int

const (
	fixtureNone fixtureType = iota
	fixtureIndex
	fixtureIndexOnly // define index var + cleanup, but don't pre-create
	fixtureDoc
	fixtureComponentTemplate
	fixtureIndexTemplate
	fixtureLegacyTemplate
	fixtureAlias
	fixtureWriteAlias
	fixturePipeline
	fixtureScript
	fixtureDataStream
)

// fixtureNeedsName reports whether the given fixture kind creates a named
// resource (template, alias) that requires the `name` variable to be declared
// in the generated test. Fixture kinds that only use `index` or `docID` return
// false.
func fixtureNeedsName(kind fixtureType) bool {
	switch kind {
	case fixtureComponentTemplate, fixtureIndexTemplate, fixtureLegacyTemplate,
		fixtureAlias, fixtureWriteAlias:
		return true
	}
	return false
}

// hasRequiredStringParam reports whether the operation has any required query
// parameter of string or list kind. These parameters use the `name` variable
// in generated integration tests, so its presence means NeedsName must be true.
// Duration, bool, and int params use literal values and don't need `name`.
func hasRequiredStringParam(op *ir.Operation) bool {
	for _, p := range op.QueryParams {
		if !p.Required {
			continue
		}
		switch p.Kind {
		case ir.ParamDuration, ir.ParamBool, ir.ParamInt:
			continue
		default:
			return true
		}
	}
	return false
}

// fixtureUsesDocID reports whether the fixture kind uses the `docID` variable.
func fixtureUsesDocID(kind fixtureType) bool {
	switch kind {
	case fixtureDoc, fixturePipeline, fixtureScript:
		return true
	}
	return false
}

// hasOptionalNameField reports whether the operation has any optional path
// field that is not Index or ID. These fields are populated with the `name`
// variable in generated tests to exercise longer URL variants.
func hasOptionalNameField(op *ir.Operation) bool {
	for _, f := range op.PathBuilder.Fields {
		if f.Required {
			continue
		}
		switch f.Name {
		case "Index", "ID", "DocumentID", "NewIndex", "Context":
			continue
		default:
			return true
		}
	}
	return false
}

func integFixtureKind(group string, hasRequiredIndex, hasRequiredID bool) fixtureType {
	switch group {
	// Ops that themselves create the resource - just need the index variable defined.
	case "indices.create":
		return fixtureIndexOnly

	// Component template operations need a component template.
	case "cluster.get_component_template", "cluster.delete_component_template",
		"cluster.exists_component_template":
		return fixtureComponentTemplate

	// Index template operations need an index template.
	case "indices.get_index_template", "indices.delete_index_template",
		"indices.exists_index_template", "indices.simulate_index_template",
		"indices.simulate_template":
		return fixtureIndexTemplate

	// Legacy template operations.
	case "indices.get_template", "indices.delete_template", "indices.exists_template":
		return fixtureLegacyTemplate

	// Alias operations need an index with an alias.
	case "indices.get_alias", "indices.exists_alias", "indices.delete_alias",
		"indices.update_aliases":
		return fixtureAlias

	// put_alias needs an index (server rejects without one even though path is optional).
	case "indices.put_alias", "indices.put_settings":
		return fixtureIndex

	// Rollover needs a write alias.
	case "indices.rollover":
		return fixtureWriteAlias

	// Pipeline operations.
	case "ingest.get_pipeline", "ingest.delete_pipeline", "ingest.simulate":
		return fixturePipeline

	// Multi-get needs a document to retrieve.
	case "mget":
		return fixtureDoc

	// Field capabilities needs an index with mapped fields.
	case "field_caps":
		return fixtureDoc

	// Script operations.
	case "get_script", "delete_script":
		return fixtureScript

	// Data stream operations need an index template + data stream.
	case "indices.get_data_stream", "indices.delete_data_stream",
		"indices.data_streams_stats":
		return fixtureDataStream
	}

	// Default: use doc fixture if ID required, index fixture if index required.
	if hasRequiredID {
		return fixtureDoc
	}
	if hasRequiredIndex {
		return fixtureIndex
	}
	return fixtureNone
}

func integBodyOverride(group string) string {
	switch group {
	case "delete_by_query", "update_by_query":
		return `strings.NewReader("{\"query\":{\"match_all\":{}}}")`
	case "update":
		return `strings.NewReader("{\"doc\":{\"title\":\"updated\"}}")`
	case "explain", "_core.explain":
		return `strings.NewReader("{\"query\":{\"match_all\":{}}}")`
	case "search", "_core.search":
		return `strings.NewReader("{\"query\":{\"match_all\":{}}}")`
	case "search_template":
		return `strings.NewReader("{\"source\":\"{\\\"query\\\":{\\\"match_all\\\":{}}}\",\"params\":{}}")`
	case "indices.put_settings":
		return `strings.NewReader("{\"index\":{\"number_of_replicas\":\"1\"}}")`
	case "indices.put_mapping":
		return `strings.NewReader("{\"properties\":{\"title\":{\"type\":\"text\"}}}")`
	case "indices.analyze":
		return `strings.NewReader("{\"text\":\"hello world\"}")`
	case "cluster.put_settings":
		return `strings.NewReader("{\"persistent\":{\"cluster.max_shards_per_node\":\"3000\"}}")`
	case "cluster.put_component_template":
		return `strings.NewReader("{\"template\":{\"mappings\":{\"properties\":{\"title\":{\"type\":\"text\"}}}}}")`
	case "indices.put_index_template":
		return `strings.NewReader("{\"index_patterns\":[\"" + name + "-*\"],\"template\":{\"settings\":{\"number_of_shards\":\"1\"}}}")`
	case "indices.put_template":
		return `strings.NewReader("{\"index_patterns\":[\"test-legacy-*\"]}")`
	case "indices.simulate_index_template", "indices.simulate_template":
		return `strings.NewReader("{\"index_patterns\":[\"test-sim-*\"],\"template\":{\"settings\":{\"number_of_shards\":\"1\"}}}")`
	case "indices.rollover":
		return `strings.NewReader("{\"conditions\":{\"max_docs\":1000}}")`
	case "indices.put_alias":
		return ""
	case "indices.update_aliases":
		return `strings.NewReader("{\"actions\":[{\"add\":{\"index\":\"" + index + "\",\"alias\":\"test-alias-2\"}}]}")`
	case "cluster.allocation_explain":
		return ""
	case "mget":
		return `strings.NewReader("{\"docs\":[{\"_index\":\"" + index + "\",\"_id\":\"" + docID + "\"}]}")`
	case "ingest.simulate":
		return `strings.NewReader("{\"pipeline\":{\"processors\":[{\"uppercase\":{\"field\":\"title\"}}]},\"docs\":[{\"_source\":{\"title\":\"hello\"}}]}")`
	case "ingest.put_pipeline":
		return `strings.NewReader("{\"description\":\"test\",\"processors\":[{\"uppercase\":{\"field\":\"title\"}}]}")`
	case "put_script":
		return `strings.NewReader("{\"script\":{\"lang\":\"painless\",\"source\":\"doc['title'].value\"}}")`
	}
	return ""
}

func buildComponentTemplateFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Cluster.PutComponentTemplate(t.Context(), %s.ClusterPutComponentTemplateReq{
		Name: name,
		Body: strings.NewReader(`+"`"+`{"template":{"mappings":{"properties":{"title":{"type":"text"}}}}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Cluster.DeleteComponentTemplate(t.Context(), %s.ClusterDeleteComponentTemplateReq{Name: name})
	})`, c, corePkg, c, corePkg)
}

func buildIndexTemplateFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.PutIndexTemplate(t.Context(), %s.IndicesPutIndexTemplateReq{
		Name: name,
		Body: strings.NewReader(`+"`"+`{"index_patterns":["` + "`" + ` + name + ` + "`" + `-*"],"template":{"settings":{"number_of_shards":"1"}}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteIndexTemplate(t.Context(), %s.IndicesDeleteIndexTemplateReq{Name: name})
	})`, c, corePkg, c, corePkg)
}

func buildLegacyTemplateFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.PutTemplate(t.Context(), %s.IndicesPutTemplateReq{
		Name: name,
		Body: strings.NewReader(`+"`"+`{"index_patterns":["` + "`" + ` + name + ` + "`" + `-*"]}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteTemplate(t.Context(), %s.IndicesDeleteTemplateReq{Name: name})
	})`, c, corePkg, c, corePkg)
}

func buildAliasFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.Create(t.Context(), %s.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	_, err = %s.Indices.PutAlias(t.Context(), %s.IndicesPutAliasReq{
		Index: []string{index},
		Name: name,
	})
	require.NoError(t, err)`, c, corePkg, c, corePkg)
}

func buildWriteAliasFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`index = index + "-000001"
	_, err = %s.Indices.Create(t.Context(), %s.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	_, err = %s.Indices.PutAlias(t.Context(), %s.IndicesPutAliasReq{
		Index: []string{index},
		Name: name,
		Body: strings.NewReader(`+"`"+`{"is_write_index":true}`+"`"+`),
	})
	require.NoError(t, err)`, c, corePkg, c, corePkg)
}

func buildPipelineFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Ingest.PutPipeline(t.Context(), %s.IngestPutPipelineReq{
		ID: docID,
		Body: strings.NewReader(`+"`"+`{"description":"test","processors":[{"uppercase":{"field":"title"}}]}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Ingest.DeletePipeline(t.Context(), %s.IngestDeletePipelineReq{ID: docID})
	})`, c, corePkg, c, corePkg)
}

func buildScriptFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.PutScript(t.Context(), %s.PutScriptReq{
		ID: docID,
		Body: strings.NewReader(`+"`"+`{"script":{"lang":"painless","source":"doc['title'].value"}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.DeleteScript(t.Context(), %s.DeleteScriptReq{ID: docID})
	})`, c, corePkg, c, corePkg)
}

func buildDataStreamFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`dsTemplate := index + "-tpl"
	dsName := index + "-ds"
	_, err = %s.Indices.PutIndexTemplate(t.Context(), %s.IndicesPutIndexTemplateReq{
		Name: dsTemplate,
		Body: strings.NewReader(`+"`"+`{"index_patterns":["`+"`"+` + dsName + `+"`"+`"],"data_stream":{}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteIndexTemplate(t.Context(), %s.IndicesDeleteIndexTemplateReq{Name: dsTemplate})
	})

	_, err = %s.Indices.CreateDataStream(t.Context(), %s.IndicesCreateDataStreamReq{Name: dsName})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteDataStream(t.Context(), &%s.IndicesDeleteDataStreamReq{Name: []string{dsName}})
	})`, c, corePkg, c, corePkg, c, corePkg, c, corePkg)
}

func buildIntegParams(op *ir.Operation, pkg string) string {
	var fields []string

	// Cat and list APIs need format=json to get JSON instead of plain text.
	if (strings.HasPrefix(op.Group, "cat.") || strings.HasPrefix(op.Group, "list.")) && !op.IsNoBody {
		for _, p := range op.QueryParams {
			if p.GoName == "Format" {
				fields = append(fields, `Format: "json"`)
				break
			}
		}
	}

	// field_caps requires the fields param even though spec marks it optional.
	if op.Group == "field_caps" {
		fields = append(fields, `Fields: []string{"*"}`)
	}

	// Required query params need sensible test defaults.
	for _, p := range op.QueryParams {
		if !p.Required {
			continue
		}
		switch p.Kind {
		case ir.ParamDuration:
			fields = append(fields, p.GoName+": 5 * time.Minute")
		case ir.ParamBool:
			fields = append(fields, p.GoName+": true")
		case ir.ParamInt:
			fields = append(fields, p.GoName+": 1")
		case ir.ParamList:
			fields = append(fields, fmt.Sprintf("%s: []string{name}", p.GoName))
		default:
			fields = append(fields, fmt.Sprintf("%s: name", p.GoName))
		}
	}

	if len(fields) == 0 {
		return ""
	}
	return fmt.Sprintf("Params: %s.%sParams{%s}", pkg, op.TypePrefix, strings.Join(fields, ", "))
}

func addParamField(callExpr string, op *ir.Operation, pkg, paramStr string) string {
	if strings.Contains(callExpr, "nil)") {
		return strings.Replace(callExpr, "nil)", "&"+pkg+"."+op.TypePrefix+"Req{"+paramStr+"})", 1)
	}
	if strings.Contains(callExpr, "Req{}") {
		return strings.Replace(callExpr, "Req{}", "Req{"+paramStr+"}", 1)
	}
	if strings.Contains(callExpr, "Req{") {
		return strings.Replace(callExpr, "Req{", "Req{"+paramStr+", ", 1)
	}
	return callExpr
}

func kebabCaseIR(s string) string {
	var result strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				result.WriteByte('-')
			}
			result.WriteRune(r + 32)
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

// Routing helpers (mirror main package route.go logic).

var coreGroupPrefixes = map[string]bool{
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

func routeOp(group, outDir, pluginsDir string) (pkg, dir string) {
	prefix := groupPrefixIR(group)
	if coreGroupPrefixes[prefix] {
		return "opensearchapi", outDir
	}
	return prefix, pluginsDir + "/" + prefix
}

func importPathForGroup(group, corePkg, modulePath string) string {
	prefix := groupPrefixIR(group)
	if coreGroupPrefixes[prefix] {
		return modulePath + "/" + corePkg
	}
	return modulePath + "/" + corePkg + "/plugins/" + prefix
}

func groupPrefixIR(group string) string {
	if idx := strings.IndexByte(group, '.'); idx >= 0 {
		return group[:idx]
	}
	return ""
}

func opFilename(group string) string {
	prefix := groupPrefixIR(group)
	if coreGroupPrefixes[prefix] {
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

func hasExistingHelper(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if filepath.Ext(name) == ".go" && !strings.HasSuffix(name, "_gen.go") {
			return true
		}
	}
	return false
}
