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

// Path field name constants used in test generation to identify fields that
// require special handling (dedicated test indices, valid enum values, etc.).
const (
	fieldIndex          = "Index"
	fieldID             = "ID"
	fieldDocumentID     = "DocumentID"
	fieldNewIndex       = "NewIndex"
	fieldContext        = "Context"
	fieldMetric         = "Metric"
	fieldIndexMetric    = "IndexMetric"
	fieldNodeIDOrMetric = "NodeIDOrMetric"
)

// Struct field names emitted on generated Req types. Used in both template
// rendering (frag_req.go) and test synthesis to keep names in sync.
const (
	BodyField       = "Body"
	BodyReaderField = "BodyReader"
	BodySuffix      = "Body" // Go type name suffix (e.g. "MlRegisterModelBody")
)

// Variable name prefixes used in generated integration test call expressions.
const (
	testClientVar        = "client."
	testFailingClientVar = "failingClient."
)

// BuildConfig holds configuration for target construction.
type BuildConfig struct {
	OutDir     string
	PluginsDir string
	CorePkg    string
	ModulePath string
	SubClients []SubClient

	// PluginSubClients maps plugin package name to a per-operation-group lookup
	// of sub-client pointers. Operations not in the inner map stay flat on root
	// Client. Keyed by package name (e.g. "security"), then by operation group
	// (e.g. "security.get_action_group").
	PluginSubClients map[string]map[string]*PluginSubClient
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
		if paramsFrag := buildParamsTestFrag(op); paramsFrag != nil {
			targets = append(targets, NewParamsTestFile(dir, filePkg, basename, paramsFrag))
		}

		// Unit test file (GetRequest path tests + roundtrip dispatch tests).
		if reqFrag := buildReqTestFrag(op, filePkg, importPath, cfg.CorePkg); reqFrag != nil {
			frags := []Fragment{reqFrag}
			if rtFrag := buildRoundtripTestFrag(op, filePkg, importPath, cfg); rtFrag != nil {
				frags = append(frags, rtFrag)
			}
			targets = append(targets, &File{
				FilePath:  dir + "/" + basename + "_gen_test.go",
				Package:   filePkg + "_test",
				BuildTag:  "!integration",
				Fragments: frags,
			})
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

	// Shared param group structs (core package).
	targets = append(targets, &File{
		FilePath:  cfg.OutDir + "/shared_params_gen.go",
		Package:   cfg.CorePkg,
		Fragments: []Fragment{&SharedParamsFragment{}},
	})

	// Breadcrumbs file (core package): comment-only summary of items dropped
	// by the version-range filter. Skipped automatically when all three
	// exclusion slices are empty (the fragment renders an empty body).
	if frag := newBreadcrumbsFragment(spec.Exclusions); frag != nil {
		targets = append(targets, &File{
			FilePath:  cfg.OutDir + "/breadcrumbs_gen.go",
			Package:   cfg.CorePkg,
			Fragments: []Fragment{frag},
		})
	}

	// Dispatch test file (core operations only).
	if frag := buildDispatchTestFrag(spec.Operations, cfg.CorePkg, coreImportPath(cfg.CorePkg, cfg.ModulePath)); frag != nil {
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
		byGroup := cfg.PluginSubClients[pkg]
		if t := NewPluginClientFile(pi.dir, pkg, pi.ops, byGroup); t != nil {
			targets = append(targets, t)
		}

		// Plugin test helper file (skip if hand-written helpers already exist).
		pluginImport := importPathForGroup(pi.ops[0].Group, cfg.CorePkg, cfg.ModulePath)
		coreImport := coreImportPath(cfg.CorePkg, cfg.ModulePath)
		testDir := pi.dir + "/internal/" + pkg + "test"
		if !hasExistingHelper(testDir) {
			targets = append(targets, NewPluginTestHelperFile(testDir, pkg, pluginImport, coreImport, cfg.CorePkg))
		}
	}

	return targets
}

func buildOperationFile(dir, pkg, basename string, op *ir.Operation, reg *ir.TypeRegistry) Target {
	var frags []Fragment

	frags = append(frags, &ReqFragment{Op: op, Registry: reg})
	frags = append(frags, &ParamsFragment{Op: op, Registry: reg})
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
	if len(op.ReqBodySiblings) > 0 {
		frags = append(frags, &SiblingTypesFragment{Op: op, Types: op.ReqBodySiblings, Registry: reg})
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

func buildParamsTestFrag(op *ir.Operation) *ParamsTestFragment {
	if len(op.QueryParams) == 0 {
		return nil
	}

	hasDuration := false
	var cases []ParamTestCase
	for _, p := range op.QueryParams {
		if p.Kind == ir.ParamDuration {
			hasDuration = true
		}
		cases = append(cases, paramTestCases(p)...)
	}

	return &ParamsTestFragment{
		TypePrefix:     op.TypePrefix,
		FormatOverride: HasFormatOverride(op.Group),
		HasDuration:    hasDuration,
		Cases:          cases,
	}
}

// paramTestCases returns one or more table rows for a query param. Most kinds
// produce a single happy-path case; *bool params emit both true and false so
// the wire-level encoding of `false` is exercised (a sentinel-pointer regression
// would silently drop the param when nil-pointer means "absent").
//
// White-box tests for both the core package and plugin packages reference the
// package-local `func(b bool) *bool { return &b }(...)` literal.
func paramTestCases(p ir.QueryParam) []ParamTestCase {
	if p.Kind == ir.ParamBool {
		return []ParamTestCase{
			{
				Name:        p.WireName + "=true",
				FieldAssign: fmt.Sprintf("%s: func(b bool) *bool { return &b }(true)", p.GoName),
				WantAssign:  fmt.Sprintf("%q: %q", p.WireName, "true"),
			},
			{
				Name:        p.WireName + "=false",
				FieldAssign: fmt.Sprintf("%s: func(b bool) *bool { return &b }(false)", p.GoName),
				WantAssign:  fmt.Sprintf("%q: %q", p.WireName, "false"),
			},
		}
	}
	tc := ParamTestCase{Name: p.WireName}
	tc.FieldAssign, tc.WantAssign = paramTestValues(p)
	return []ParamTestCase{tc}
}

// paramTestValues returns the field assignment and expected map entry for one
// query param case. White-box tests for both the core package and plugin
// packages use the inline `func(b bool) *bool { return &b }(...)` literal
// emitted directly into the test source for *bool params, so no per-package
// helper is required.
func paramTestValues(p ir.QueryParam) (string, string) {
	var fieldAssign, wantAssign string
	switch p.Kind {
	case ir.ParamDuration:
		fieldAssign = fmt.Sprintf("%s: 5 * time.Second", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "5000ms")
	case ir.ParamBool:
		fieldAssign = fmt.Sprintf("%s: func(b bool) *bool { return &b }(true)", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "true")
	case ir.ParamList:
		fieldAssign = fmt.Sprintf(`%s: []string{"a", "b"}`, p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "a,b")
	case ir.ParamInt:
		fieldAssign = fmt.Sprintf("%s: 42", p.GoName)
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "42")
	case ir.ParamString:
		fallthrough
	default:
		fieldAssign = fmt.Sprintf("%s: %q", p.GoName, "test-value")
		wantAssign = fmt.Sprintf("%q: %q", p.WireName, "test-value")
	}
	return fieldAssign, wantAssign
}

func buildReqTestFrag(op *ir.Operation, pkg, importPath, corePkg string) *ReqTestFragment {
	cases := synthesizeReqCasesIR(op, pkg, corePkg)
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

func synthesizeReqCasesIR(op *ir.Operation, pkg, corePkg string) []ReqTestCase {
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
		if op.HasTypedBody {
			assigns = append(assigns, fmt.Sprintf("%s: &%s", BodyField, qualifiedBodyLiteral(op, pkg, corePkg)))
		} else {
			assigns = append(assigns, BodyField+`: strings.NewReader("{}")`)
		}
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

	// switch state: when we enter opSwitch, switchActive=true. Each
	// case/default toggles writing on/off. Once a case matches, taken=true
	// and subsequent cases (and default) skip until opSwitchEnd.
	var switchActive, taken, writing bool
	// if state: ifActive=true between opIf and opIfEnd. writing is false
	// when the if condition didn't match.
	var ifActive bool

	condsAllMatch := func(conds []ir.PathCaseCondition) bool {
		for _, c := range conds {
			if _, ok := values[c.Field]; !ok {
				return false
			}
		}
		return len(conds) > 0
	}

	for _, op := range pb.Ops {
		switch op.Kind {
		case ir.PathOpLit:
			if !switchActive && !ifActive {
				segments = append(segments, op.Value)
				continue
			}
			if writing {
				segments = append(segments, op.Value)
			}
		case ir.PathOpField, ir.PathOpList:
			emit := !switchActive && !ifActive
			if !emit {
				emit = writing
			}
			if !emit {
				continue
			}
			if v, ok := values[op.Value]; ok {
				segments = append(segments, v)
			}
		case ir.PathOpSwitch:
			switchActive = true
			taken = false
			writing = false
		case ir.PathOpCase:
			if taken {
				writing = false
				continue
			}
			if condsAllMatch(op.Conditions) {
				writing = true
				taken = true
			} else {
				writing = false
			}
		case ir.PathOpDefault:
			writing = !taken
			taken = true
		case ir.PathOpSwitchEnd:
			switchActive = false
			taken = false
			writing = false
		case ir.PathOpIf:
			ifActive = true
			writing = condsAllMatch(op.Conditions)
		case ir.PathOpIfEnd:
			ifActive = false
			writing = false
		case ir.PathOpExplainCheck:
			// Runtime-only error path: never matches valid test inputs.
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

// buildIntegCallPrefix returns the qualified function/method expression that
// the integration test should call to invoke the operation.
func buildIntegCallPrefix(op *ir.Operation, buildCfg BuildConfig, isPlugin bool) string {
	if !isPlugin {
		route := primaryRouteIR(op)
		prefix := testClientVar
		if route.FieldPath != "" {
			prefix += route.FieldPath + "."
		}
		return prefix + route.MethodName
	}
	suffix := op.Group
	if idx := strings.IndexByte(suffix, '.'); idx >= 0 {
		suffix = suffix[idx+1:]
	}
	methodName := PluginMethodName(suffix)
	prefix := groupPrefixIR(op.Group)
	if sc := buildCfg.PluginSubClients[prefix][op.Group]; sc != nil {
		return testClientVar + sc.FieldName + "." + methodName
	}
	return testClientVar + methodName
}

// optionalPathFieldAssign builds the field-assignment expression for an
// optional path field in an integration test. Returns "" when the field
// should be left unset (skip-list, or fields whose values must come from
// fixtures the test does not create).
func optionalPathFieldAssign(f ir.PathBuilderField, skip map[string]bool) string {
	if skip[f.Name] {
		return ""
	}
	switch f.Name {
	case fieldIndex:
		if f.IsList {
			return "Index: []string{index}"
		}
		return "Index: index"
	case fieldID, fieldDocumentID, fieldNewIndex, fieldContext,
		fieldMetric, fieldIndexMetric, fieldNodeIDOrMetric:
		return ""
	}
	if f.IsList {
		return fmt.Sprintf("%s: []string{name}", f.Name)
	}
	return fmt.Sprintf("%s: name", f.Name)
}

func buildIntegTestFrag(op *ir.Operation, pkg, importPath string, cfg BuildConfig) *IntegTestFragment {
	isPlugin := op.IsPlugin
	config := classifyOpIR(op, pkg, cfg.CorePkg, isPlugin, cfg)

	return &IntegTestFragment{
		PkgName:    pkg,
		ImportPath: importPath,
		ModulePath: cfg.ModulePath,
		CorePkg:    cfg.CorePkg,
		Config:     config,
	}
}

func classifyOpIR(op *ir.Operation, pkg, corePkg string, isPlugin bool, buildCfg BuildConfig) IntegTestConfig {
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

	flags, skipReason, skipVersion := MatchRules(op.Group)
	cfg.Flags = flags
	if skipReason != "" {
		cfg.SkipReason = skipReason
	}
	if skipVersion != "" {
		cfg.VersionAdded = skipVersion
	}

	callPrefix := buildIntegCallPrefix(op, buildCfg, isPlugin)

	hasRequiredIndex := false
	hasRequiredID := false
	hasOtherRequired := false
	hasOptionalIndex := false
	for _, f := range op.PathBuilder.Fields {
		if !f.Required {
			if f.Name == fieldIndex {
				hasOptionalIndex = true
			}
			continue
		}
		switch f.Name {
		case fieldIndex:
			hasRequiredIndex = true
		case fieldID, fieldDocumentID:
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

	cfg.ResourcePrefix = "test-" + kebabCaseIR(op.TypePrefix)

	// Determine fixture based on operation semantics.
	fixtureKind := integFixtureKind(op.Group, hasRequiredIndex, hasRequiredID)
	if fixtureKind == fixtureNone && hasOptionalIndex {
		fixtureKind = fixtureIndex
	}
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
	case fixtureNone:
		// no fixture needed
	}

	// Determine which unique-string variables the test needs, based on
	// operation structure and fixture kind.
	if fixtureKind == fixtureNone {
		cfg.NeedsIndex = hasRequiredIndex
	} else {
		cfg.NeedsIndex = true
	}
	cfg.NeedsDocID = hasRequiredID || fixtureUsesDocID(fixtureKind)

	bodyOverride := integBodyOverride(op.Group)

	// Only count optional name fields when the call expression will actually
	// build a non-nil request literal (otherwise the variable is unused).
	willBuildReq := hasRequiredIndex || hasRequiredID || hasOtherRequired || hasOptionalIndex || needsBody || bodyOverride != ""
	if !op.IsPointerReq {
		willBuildReq = true
	}
	cfg.NeedsName = hasOtherRequired || fixtureNeedsName(fixtureKind) ||
		hasRequiredStringParam(op) || (willBuildReq && hasOptionalNameField(op))

	cfg.CallExpr = buildCallExprIR(
		callPrefix, op, pkg, corePkg,
		hasRequiredIndex, hasRequiredID, hasOtherRequired, hasOptionalIndex,
		needsBody, isMutating, bodyOverride, false,
	)
	cfg.FailCallExpr = buildCallExprIR(
		callPrefix, op, pkg, corePkg,
		hasRequiredIndex, hasRequiredID, hasOtherRequired, hasOptionalIndex,
		needsBody, isMutating, bodyOverride, true,
	)

	// Build all necessary query params (required params + cat format=json).
	if paramStr := buildIntegParams(op, pkg, corePkg); paramStr != "" {
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

func buildCallExprIR(
	callPrefix string,
	op *ir.Operation,
	pkg, corePkg string,
	hasRequiredIndex, hasRequiredID, hasOtherRequired, hasOptionalIndex bool,
	needsBody, isMutating bool,
	bodyOverride string,
	isFailing bool,
) string {
	prefix := callPrefix
	if isFailing {
		prefix = strings.Replace(callPrefix, testClientVar, testFailingClientVar, 1)
	}

	if op.IsPointerReq {
		if !hasRequiredIndex && !hasRequiredID && !hasOtherRequired && !hasOptionalIndex && !needsBody && bodyOverride == "" {
			return prefix + "(t.Context(), nil)"
		}
		return prefix + "(t.Context(), &" +
			buildReqLiteralIR(op, pkg, corePkg, hasRequiredIndex, hasRequiredID, needsBody, isMutating, bodyOverride) + ")"
	}
	return prefix + "(t.Context(), " +
		buildReqLiteralIR(op, pkg, corePkg, hasRequiredIndex, hasRequiredID, needsBody, isMutating, bodyOverride) + ")"
}

func buildReqLiteralIR(
	op *ir.Operation,
	pkg, corePkg string,
	hasRequiredIndex, hasRequiredID, needsBody, isMutating bool,
	bodyOverride string,
) string {
	var fields []string

	// Populate all path fields in the test request for a comprehensive happy-path
	// exercise. Optional fields are included too, since they exercise longer URL
	// variants and scope operations to dedicated test indices.

	// Skip optional dependent fields whose predecessor is unpopulated: the
	// generated Build() rejects such combinations with errRequired and the
	// predecessor is in the "specific valid values" exclusion list.
	skip := unsatisfiedPositionalDeps(op.PathBuilder)

	for _, f := range op.PathBuilder.Fields {
		if !f.Required {
			if v := optionalPathFieldAssign(f, skip); v != "" {
				fields = append(fields, v)
			}
			continue
		}
		switch f.Name {
		case fieldIndex:
			if f.IsList {
				fields = append(fields, "Index: []string{index}")
			} else {
				fields = append(fields, "Index: index")
			}
		case fieldID, fieldDocumentID:
			fields = append(fields, f.Name+": docID")
		default:
			if f.IsList {
				fields = append(fields, fmt.Sprintf("%s: []string{name}", f.Name))
			} else {
				fields = append(fields, fmt.Sprintf("%s: name", f.Name))
			}
		}
	}

	if v := integBodyAssign(op, pkg, corePkg, bodyOverride, needsBody); v != "" {
		fields = append(fields, v)
	}

	if isMutating && hasRequiredIndex && !hasRequiredID && hasRefreshParamIR(op) {
		fields = append(fields, fmt.Sprintf("Params: &%s.%sParams{Refresh: %q}", pkg, op.TypePrefix, "true"))
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

// integBodyAssign returns the field-assignment expression for the request
// body in an integration test, or "" when no body field is needed.
func integBodyAssign(op *ir.Operation, pkg, corePkg, bodyOverride string, needsBody bool) string {
	switch {
	case bodyOverride != "" && op.HasTypedBody:
		return BodyReaderField + ": " + bodyOverride
	case bodyOverride != "":
		return BodyField + ": " + bodyOverride
	case needsBody && op.HasTypedBody:
		return fmt.Sprintf("%s: &%s", BodyField, qualifiedBodyLiteral(op, pkg, corePkg))
	case needsBody:
		return BodyField + `: strings.NewReader("{}")`
	}
	return ""
}

// unsatisfiedPositionalDeps returns the set of optional dependent fields
// whose predecessor is in the test-fixture exclusion list (Metric,
// IndexMetric, NodeIDOrMetric, etc.). Populating the dependent without the
// predecessor would trip the Build() positional guard at runtime, so the
// caller skips them entirely.
func unsatisfiedPositionalDeps(pb ir.PathBuilder) map[string]bool {
	skipped := map[string]bool{
		fieldMetric:         true,
		fieldIndexMetric:    true,
		fieldNodeIDOrMetric: true,
	}
	out := make(map[string]bool)
	for _, dep := range pb.PositionalDeps {
		if skipped[dep.Predecessor.Name] {
			out[dep.Dependent.Name] = true
		}
	}
	return out
}

// qualifiedBodyLiteral returns the Go expression for an empty body struct
// literal, qualified for use in external test packages (e.g. "opensearchapi.CountBody{}").
// For plugin operations whose body type lives in the core package, the core
// package qualifier is used instead.
func qualifiedBodyLiteral(op *ir.Operation, pkg, corePkg string) string {
	if op.RequestBody == nil {
		return pkg + "." + op.TypePrefix + BodySuffix + "{}"
	}
	name := op.RequestBody.Name
	if op.IsPlugin && op.RequestBody.Scope == ir.ScopeShared {
		return corePkg + "." + name + "{}"
	}
	return pkg + "." + name + "{}"
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
		Params: &%s.IndexParams{Refresh: "true"},
	})
	require.NoError(t, err)`, c, corePkg, c, corePkg, corePkg)
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
	case fixtureNone, fixtureIndex, fixtureIndexOnly, fixtureDoc,
		fixturePipeline, fixtureScript, fixtureDataStream:
		return false
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
		case ir.ParamString, ir.ParamList:
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
	case fixtureNone, fixtureIndex, fixtureIndexOnly, fixtureComponentTemplate,
		fixtureIndexTemplate, fixtureLegacyTemplate, fixtureAlias,
		fixtureWriteAlias, fixtureDataStream:
		return false
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
		case fieldIndex, fieldID, fieldDocumentID, fieldNewIndex, fieldContext,
			fieldMetric, fieldIndexMetric, fieldNodeIDOrMetric:
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
		return `strings.NewReader("{` +
			`\"pipeline\":{\"processors\":[{\"uppercase\":{\"field\":\"title\"}}]},` +
			`\"docs\":[{\"_source\":{\"title\":\"hello\"}}]}")`
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
		BodyReader: strings.NewReader(`+"`"+`{"template":{"mappings":{"properties":{"title":{"type":"text"}}}}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Cluster.DeleteComponentTemplate(context.Background(), %s.ClusterDeleteComponentTemplateReq{Name: name})
	})`, c, corePkg, c, corePkg)
}

func buildIndexTemplateFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.PutIndexTemplate(t.Context(), %s.IndicesPutIndexTemplateReq{
		Name: name,
		BodyReader: strings.NewReader(`+"`"+`{"index_patterns":["`+"`"+` + name + `+"`"+
		`-*"],"template":{"settings":{"number_of_shards":"1"}}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteIndexTemplate(context.Background(), %s.IndicesDeleteIndexTemplateReq{Name: name})
	})`, c, corePkg, c, corePkg)
}

func buildLegacyTemplateFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.Indices.PutTemplate(t.Context(), %s.IndicesPutTemplateReq{
		Name: name,
		BodyReader: strings.NewReader(`+"`"+`{"index_patterns":["`+"`"+` + name + `+"`"+`-*"]}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteTemplate(context.Background(), %s.IndicesDeleteTemplateReq{Name: name})
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
	return fmt.Sprintf(`index += "-000001"
	_, err = %s.Indices.Create(t.Context(), %s.IndicesCreateReq{Index: index})
	require.NoError(t, err)

	_, err = %s.Indices.PutAlias(t.Context(), %s.IndicesPutAliasReq{
		Index: []string{index},
		Name: name,
		BodyReader: strings.NewReader(`+"`"+`{"is_write_index":true}`+"`"+`),
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
		BodyReader: strings.NewReader(`+"`"+`{"description":"test","processors":[{"uppercase":{"field":"title"}}]}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Ingest.DeletePipeline(context.Background(), %s.IngestDeletePipelineReq{ID: docID})
	})`, c, corePkg, c, corePkg)
}

func buildScriptFixtureIR(corePkg string, isPlugin bool) string {
	c := "client"
	if isPlugin {
		c = "osClient"
	}
	return fmt.Sprintf(`_, err = %s.PutScript(t.Context(), %s.PutScriptReq{
		ID: docID,
		BodyReader: strings.NewReader(`+"`"+`{"script":{"lang":"painless","source":"doc['title'].value"}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.DeleteScript(context.Background(), %s.DeleteScriptReq{ID: docID})
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
		BodyReader: strings.NewReader(`+"`"+`{"index_patterns":["`+"`"+` + dsName + `+"`"+`"],"data_stream":{}}`+"`"+`),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteIndexTemplate(context.Background(), %s.IndicesDeleteIndexTemplateReq{Name: dsTemplate})
	})

	_, err = %s.Indices.CreateDataStream(t.Context(), %s.IndicesCreateDataStreamReq{Name: dsName})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = %s.Indices.DeleteDataStream(context.Background(), &%s.IndicesDeleteDataStreamReq{Name: []string{dsName}})
	})`, c, corePkg, c, corePkg, c, corePkg, c, corePkg)
}

func buildIntegParams(op *ir.Operation, pkg, corePkg string) string {
	var fields []string

	// Cat and list APIs need format=json to get JSON instead of plain text.
	// Format lives in the embedded DebugParams, accessible via field promotion.
	if (strings.HasPrefix(op.Group, "cat.") || strings.HasPrefix(op.Group, "list.")) && !op.IsNoBody {
		fields = append(fields, `DebugParams: `+corePkg+`.DebugParams{Format: "json"}`)
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
			// Integ tests use a local bool var and pointer for required bool params.
			fields = append(fields, fmt.Sprintf("%s: func(b bool) *bool { return &b }(true)", p.GoName))
		case ir.ParamInt:
			fields = append(fields, p.GoName+": 1")
		case ir.ParamList:
			fields = append(fields, fmt.Sprintf("%s: []string{name}", p.GoName))
		case ir.ParamString:
			fallthrough
		default:
			fields = append(fields, fmt.Sprintf("%s: name", p.GoName))
		}
	}

	if len(fields) == 0 {
		return ""
	}
	return fmt.Sprintf("Params: &%s.%sParams{%s}", pkg, op.TypePrefix, strings.Join(fields, ", "))
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

// coreGroupPrefixes are operation-group prefixes that belong in the core
// package (mirrors the main package's coreGroups). Keys are sorted
// alphabetically; keep them that way when adding entries.
//
//nolint:gochecknoglobals // const-ish read-only lookup table
var coreGroupPrefixes = map[string]bool{
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

func routeOp(group, outDir, pluginsDir string) (string, string) {
	prefix := groupPrefixIR(group)
	if coreGroupPrefixes[prefix] {
		return "opensearchapi", outDir
	}
	return prefix, pluginsDir + "/" + prefix
}

func importPathForGroup(group, corePkg, modulePath string) string {
	prefix := groupPrefixIR(group)
	core := coreImportPath(corePkg, modulePath)
	if coreGroupPrefixes[prefix] {
		return core
	}
	return core + "/plugins/" + prefix
}

// coreImportPath returns the full import path for the core API package.
// When corePkg matches the default name, it uses the canonical subpath
// (currently nested under v5preview/); otherwise it places the override
// package directly under the module root for legacy compatibility.
func coreImportPath(corePkg, modulePath string) string {
	if corePkg == ir.DefaultCorePkgName {
		return modulePath + "/" + ir.DefaultCoreSubpath
	}
	return modulePath + "/" + corePkg
}

func groupPrefixIR(group string) string {
	if before, _, ok := strings.Cut(group, "."); ok {
		return before
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
	if _, after, ok := strings.Cut(group, "."); ok {
		return after
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

func buildRoundtripTestFrag(op *ir.Operation, pkg, importPath string, _ BuildConfig) *RoundtripTestFragment {
	if op.IsPlugin {
		return nil
	}

	route := primaryRouteIR(op)
	callPrefix := "client."
	if route.FieldPath != "" {
		callPrefix += route.FieldPath + "."
	}
	callPrefix += route.MethodName

	errPrefix := "errClient."
	if route.FieldPath != "" {
		errPrefix += route.FieldPath + "."
	}
	errPrefix += route.MethodName

	hasRequired := false
	for _, f := range op.PathFields {
		if f.Required {
			hasRequired = true
			break
		}
	}

	primary := PrimaryMethod(op)
	needsBody := op.HasBody && primary != http.MethodGet

	var reqExpr string
	if op.IsPointerReq {
		if !hasRequired && !needsBody {
			reqExpr = "nil"
		} else {
			reqExpr = "&" + buildRoundtripReqLiteral(op, pkg, needsBody)
		}
	} else {
		reqExpr = buildRoundtripReqLiteral(op, pkg, needsBody)
	}

	callExpr := callPrefix + "(t.Context(), " + reqExpr + ")"
	errCallExpr := errPrefix + "(t.Context(), " + reqExpr + ")"

	needsStrings := strings.Contains(reqExpr, "strings.NewReader")

	var fixture string
	switch op.RespShape {
	case ir.RespShapeArray:
		fixture = "[]"
	case ir.RespShapeStruct, ir.RespShapeMap, ir.RespShapeRaw:
		fixture = "{}"
	}
	if op.IsNoBody {
		fixture = ""
	}

	return &RoundtripTestFragment{
		PkgName:      pkg,
		ImportPath:   importPath,
		TypePrefix:   op.TypePrefix,
		RespFixture:  fixture,
		IsNoBody:     op.IsNoBody,
		CallExpr:     callExpr,
		ErrCallExpr:  errCallExpr,
		NeedsBody:    needsBody,
		NeedsStrings: needsStrings,
	}
}

func buildRoundtripReqLiteral(op *ir.Operation, pkg string, needsBody bool) string {
	var fields []string
	for _, f := range op.PathFields {
		if !f.Required {
			continue
		}
		if f.IsList {
			fields = append(fields, fmt.Sprintf(`%s: []string{"test"}`, f.GoName))
		} else {
			fields = append(fields, fmt.Sprintf(`%s: "test"`, f.GoName))
		}
	}
	if needsBody {
		if op.HasTypedBody {
			fields = append(fields, fmt.Sprintf(`%s: strings.NewReader("{}")`, BodyReaderField))
		} else {
			fields = append(fields, fmt.Sprintf(`%s: strings.NewReader("{}")`, BodyField))
		}
	}
	return fmt.Sprintf("%s.%sReq{%s}", pkg, op.TypePrefix, strings.Join(fields, ", "))
}
