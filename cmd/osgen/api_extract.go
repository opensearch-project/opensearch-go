// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// Content types and schema key suffixes used during extraction.
const (
	contentJSON   = "application/json"
	contentNDJSON = "application/x-ndjson"

	// Schema key suffixes appended to the group name when a schema is inline
	// (not a $ref to components/schemas/).
	respBodySuffix = "___ResponseBody"
	reqBodySuffix  = "___Body"
)

// apiOperation holds everything needed to generate one set of API files.
type apiOperation struct {
	Group           string
	TypePrefix      string
	PathBuilderName string

	// HTTPMethods lists all valid HTTP methods for this operation. The first
	// element is always the primary method (from the primary/shortest path
	// variant in the spec); remaining methods are sorted alphabetically.
	// For [GET, POST] operations with a body, POST is used when Body != nil.
	HTTPMethods []string

	PrimaryPath string // canonical URL path pattern (e.g. "/{index}/_refresh")

	Description string
	VersionAdded      string
	VersionDeprecated string
	Deprecated        bool
	DeprecationMsg    string
	DocsURL           string
	ExcludedDistros   []string
	HasBody           bool
	PathFields        []apiPathField
	QueryParams       []apiQueryParam
	ResponseRef       string // schema key for the 200 response body (e.g. "cluster.health___HealthResponseBody")

	// ResponseSchemaRef is the resolved schema for the 200 response body,
	// used to walk inline schemas that aren't in Components.Schemas.
	ResponseSchemaRef *openapi3.SchemaRef

	RequestRef       string             // schema key for request body (e.g. "ml.register_model___Body")
	RequestSchemaRef *openapi3.SchemaRef // resolved request body schema
	IsNDJSON         bool               // true for application/x-ndjson request bodies

	// Dispatch routes for this operation (primary flat + optional deprecated nested).
	DispatchRoutes []dispatchRoute
	IsPointerReq   bool // true when all path fields are optional (pointer req convention)
	IsNoBody       bool // true for HEAD-only operations (returns *opensearch.Response)

	// PathBuilder holds the analyzed trie ops for path construction, used by
	// test generation to simulate Build() without importing internal/path.
	PathBuilder builder

	// Populated after schema walking.
	RespFields    []goField      // fields for the top-level Resp struct
	SiblingTypes  []*goType      // operation-specific types emitted alongside Resp
	RespShape     ir.RespShape   // overall response body structure (struct/map/array/raw)
	RespElemType  *goType        // element type for map/array shapes (the T in map[string]T or []T)

	// Populated after request body schema walking.
	ReqBodyFields    []goField  // fields for the top-level Body struct
	ReqBodySiblings  []*goType  // operation-specific types from request body
	ReqBodyTypeName  string     // Go type name for the body struct (e.g. "MlRegisterModelBody")
	ReqBodyIsShared  bool       // true when body type is shared (emitted in types_gen.go)
	HasTypedBody     bool       // true when body has properties (typed struct, not io.Reader)
}

// apiPathField is one path parameter exposed as a struct field on the Req.
// The generated Req struct includes one field per URL path placeholder.
type apiPathField struct {
	GoName   string // exported Go field name (e.g. "IndexUUID")
	WireName string // original OpenAPI parameter name (e.g. "index_uuid")
	IsList   bool   // true if the parameter accepts comma-separated values
}

// apiQueryParam is one query parameter exposed on the Params struct.
// Each field maps to a URL query key in the OpenSearch REST API.
type apiQueryParam struct {
	GoName            string // exported Go field name
	ParamName         string // wire name used in the query string (e.g. "wait_for_active_shards")
	GoType            string // Go type for the field (e.g. "string", "bool", "time.Duration")
	Description       string // human-readable description from the spec
	Default           string // server default value (e.g. "true", "30s")
	IsDuration        bool   // true if the value encodes as an OpenSearch duration string
	IsBool            bool
	IsList            bool // true if the value is comma-joined ([]string)
	IsInt             bool
	Required          bool
	Deprecated        bool
	VersionAdded      string // semver when this param was introduced
	VersionDeprecated string // semver when this param was deprecated
	DeprecationMsg    string // explains what to use instead
}

// extractOperations parses the OpenAPI spec and returns one apiOperation per
// x-operation-group. An optional filter restricts output to named groups. It
// also returns the loaded spec for use by the response schema walker, and the
// version-filter exclusions (operations and query params) so the caller can
// emit breadcrumb comments for items dropped by --min-version/--max-version.
func extractOperations(
	specPath string, filter map[string]bool, vrange VersionRange,
) ([]apiOperation, *openapi3.T, []ir.Exclusion, []ir.Exclusion, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("loading spec: %w", err)
	}

	type groupData struct {
		ops []struct {
			method string
			op     *openapi3.Operation
			path   *openapi3.PathItem
			url    string
		}
	}

	grouped := make(map[string]*groupData)
	var (
		opExclusions    []ir.Exclusion
		opExclusionSeen = make(map[string]bool) // dedupe by group name
	)

	// Iterate paths and methods in sorted order so that exclusion order is
	// deterministic across runs (Go's randomized map iteration would otherwise
	// shuffle the breadcrumb output).
	pathKeys := make([]string, 0, len(spec.Paths.Map()))
	for k := range spec.Paths.Map() {
		pathKeys = append(pathKeys, k)
	}
	sort.Strings(pathKeys)

	for _, urlPath := range pathKeys {
		pathItem := spec.Paths.Map()[urlPath]
		opMap := pathItem.Operations()
		methods := make([]string, 0, len(opMap))
		for m := range opMap {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := opMap[method]
			group := operationGroup(op)
			if group == "" {
				continue
			}
			if extensionBool(op.Extensions, extIgnorable) {
				continue
			}
			vAdded := extensionString(op.Extensions, extVersionAdded)
			vRemoved := extensionString(op.Extensions, extVersionRemoved)
			vDeprecated := extensionString(op.Extensions, extVersionDeprecated)
			if !vrange.Includes(vAdded, vRemoved, vDeprecated) {
				if filter != nil && !filter[group] {
					continue
				}
				if opExclusionSeen[group] {
					continue
				}
				if exc := vrange.Exclusion(group, vAdded, vRemoved, vDeprecated); exc != nil {
					opExclusions = append(opExclusions, ir.Exclusion{
						Name:    exc.Name,
						Reason:  exc.Reason,
						IsOlder: exc.IsOlder,
					})
					opExclusionSeen[group] = true
				}
				continue
			}
			if filter != nil && !filter[group] {
				continue
			}

			g, ok := grouped[group]
			if !ok {
				g = &groupData{}
				grouped[group] = g
			}
			g.ops = append(g.ops, struct {
				method string
				op     *openapi3.Operation
				path   *openapi3.PathItem
				url    string
			}{method: method, op: op, path: pathItem, url: urlPath})
		}
	}

	result := make([]apiOperation, 0, len(grouped))
	var paramExclusions []ir.Exclusion
	for group, gd := range grouped {
		op, pExc := buildAPIOperation(group, gd.ops, spec, vrange)
		result = append(result, op)
		paramExclusions = append(paramExclusions, pExc...)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Group < result[j].Group
	})
	sort.Slice(opExclusions, func(i, j int) bool { return opExclusions[i].Name < opExclusions[j].Name })
	sort.Slice(paramExclusions, func(i, j int) bool { return paramExclusions[i].Name < paramExclusions[j].Name })
	return result, spec, opExclusions, paramExclusions, nil
}

// buildAPIOperation constructs an apiOperation from all path variants sharing
// a group name. It picks the primary (non-deprecated) variant for metadata and
// merges query parameters from all variants. Returns the operation and any
// query-param exclusions produced by the version-range filter.
func buildAPIOperation(group string, ops []struct {
	method string
	op     *openapi3.Operation
	path   *openapi3.PathItem
	url    string
}, _ *openapi3.T, vrange VersionRange,
) (apiOperation, []ir.Exclusion) {
	// Sort ops by URL for determinism, then by operationId within the same URL
	// to preserve the spec's declared primary ordering (e.g. search.0 is POST,
	// search.1 is GET - the spec author chose .0 as primary).
	sort.Slice(ops, func(i, j int) bool {
		if ops[i].url != ops[j].url {
			return ops[i].url < ops[j].url
		}
		return ops[i].op.OperationID < ops[j].op.OperationID
	})

	// Pick the primary (non-deprecated) operation for metadata.
	var primary struct {
		method string
		op     *openapi3.Operation
		path   *openapi3.PathItem
		url    string
	}
	for _, o := range ops {
		if !o.op.Deprecated {
			primary = o
			break
		}
	}
	if primary.op == nil {
		primary = ops[0]
	}

	op := primary.op
	typePrefix := pkgScopedName(group)

	var docsURL string
	if op.ExternalDocs != nil {
		docsURL = op.ExternalDocs.URL
	}

	allDeprecated := true
	for _, o := range ops {
		if !o.op.Deprecated {
			allDeprecated = false
			break
		}
	}

	// Collect all distinct HTTP methods across variants of this operation,
	// with the primary method first and the rest sorted.
	primaryMeth := strings.ToUpper(primary.method)
	methodSet := make(map[string]struct{})
	for _, o := range ops {
		methodSet[strings.ToUpper(o.method)] = struct{}{}
	}
	methods := make([]string, 0, len(methodSet))
	methods = append(methods, primaryMeth)
	delete(methodSet, primaryMeth)
	rest := make([]string, 0, len(methodSet))
	for m := range methodSet {
		rest = append(rest, m)
	}
	sort.Strings(rest)
	methods = append(methods, rest...)

	apiOp := apiOperation{
		Group:             group,
		TypePrefix:        typePrefix,
		PathBuilderName:   pathBuilderName(group),
		HTTPMethods:       methods,
		PrimaryPath:       primary.url,
		Description:       op.Description,
		VersionAdded:      extensionString(op.Extensions, extVersionAdded),
		VersionDeprecated: extensionString(op.Extensions, extVersionDeprecated),
		Deprecated:        allDeprecated,
		DeprecationMsg:    deprecationMessage(op),
		DocsURL:           docsURL,
		ExcludedDistros:   extensionStringSlice(op.Extensions, extDistributionsExcluded),
		HasBody:           op.RequestBody != nil,
	}

	// Extract request body schema ref.
	if op.RequestBody != nil && op.RequestBody.Value != nil && op.RequestBody.Value.Content != nil {
		if mt := op.RequestBody.Value.Content.Get(contentNDJSON); mt != nil {
			apiOp.IsNDJSON = true
		} else if mt := op.RequestBody.Value.Content.Get(contentJSON); mt != nil && mt.Schema != nil {
			apiOp.RequestRef = refToSchemaKey(mt.Schema.Ref)
			if apiOp.RequestRef == "" {
				apiOp.RequestRef = group + reqBodySuffix
			}
			apiOp.RequestSchemaRef = mt.Schema
		}
	}

	// Union path fields across all URL variants. Union path parameters
	// (anyOf members) are dropped from the request struct: their concrete
	// values are passed through the member fields, and Build() picks the
	// right path variant from which fields are populated.
	unionParams := collectUnionParams(ops)
	seenPath := make(map[string]bool)
	for _, o := range ops {
		pathParams := extractPathParams(o.path, o.op, o.url)
		for _, pp := range pathParams {
			if _, isUnion := unionParams[pp.name]; isUnion {
				continue
			}
			goName := pathFieldName(pp.name)
			if seenPath[goName] {
				continue
			}
			seenPath[goName] = true
			apiOp.PathFields = append(apiOp.PathFields, apiPathField{
				GoName:   goName,
				WireName: pp.name,
				IsList:   pp.isList,
			})
		}
	}

	// Build path trie for test generation (same logic as paths subcommand).
	// Group by URL path and collect all HTTP methods for each path.
	type variantData struct {
		params      []string
		arrayParams map[string]bool
		deprecated  bool
		methods     map[string]struct{}
	}
	variantsByURL := make(map[string]*variantData)
	variantOrder := make([]string, 0, len(ops))
	for _, o := range ops {
		if vd, ok := variantsByURL[o.url]; ok {
			vd.methods[strings.ToUpper(o.method)] = struct{}{}
			if !o.op.Deprecated {
				vd.deprecated = false
			}
			continue
		}
		params := extractPathParams(o.path, o.op, o.url)
		paramNames := make([]string, 0, len(params))
		arrayParams := make(map[string]bool)
		for _, pp := range params {
			paramNames = append(paramNames, pp.name)
			if pp.isList {
				arrayParams[pp.name] = true
			}
		}
		variantsByURL[o.url] = &variantData{
			params:      paramNames,
			arrayParams: arrayParams,
			deprecated:  o.op.Deprecated,
			methods:     map[string]struct{}{strings.ToUpper(o.method): {}},
		}
		variantOrder = append(variantOrder, o.url)
	}
	variants := make([]pathVariant, 0, len(variantsByURL))
	for _, url := range variantOrder {
		vd := variantsByURL[url]
		variants = append(variants, pathVariant{
			path:        url,
			methods:     vd.methods,
			pathParams:  vd.params,
			arrayParams: vd.arrayParams,
			deprecated:  vd.deprecated,
		})
	}
	if b, err := analyzeGroup(opGroup{name: group, pathSpecs: variants, unionParams: unionParams}); err == nil {
		b.export()
		apiOp.PathBuilder = b
	}

	// Union query parameters across all variants.
	queryParams, paramExc := extractQueryParamsUnion(group, ops, vrange)
	apiOp.QueryParams = queryParams

	// Extract response schema ref from the first variant with a 200 response.
	for _, o := range ops {
		if o.op.Responses == nil {
			continue
		}
		resp := o.op.Responses.Value("200")
		if resp == nil || resp.Value == nil || resp.Value.Content == nil {
			continue
		}
		mt := resp.Value.Content.Get(contentJSON)
		if mt == nil || mt.Schema == nil {
			continue
		}
		apiOp.ResponseRef = refToSchemaKey(mt.Schema.Ref)
		if apiOp.ResponseRef == "" {
			apiOp.ResponseRef = group + respBodySuffix
		}
		apiOp.ResponseSchemaRef = mt.Schema
		break
	}

	// Resolve dispatch routes and request style.
	apiOp.DispatchRoutes = resolveDispatchRoutes(group)
	apiOp.IsPointerReq = !hasRequiredScalarPath(apiOp.PathFields)
	apiOp.IsNoBody = len(methods) == 1 && methods[0] == http.MethodHead

	return apiOp, paramExc
}

type pathParam struct {
	name   string
	isList bool
}

// collectUnionParams gathers union path parameters across every operation
// variant in a group. The result keys are synthetic param names (e.g.
// "node_id_or_metric"); values are the anyOf member titles. Used by
// buildAPIOperation to drop the synthetic param from the request struct
// and by analyzeGroup to expand path variants for Build() emission.
func collectUnionParams(ops []struct {
	method string
	op     *openapi3.Operation
	path   *openapi3.PathItem
	url    string
},
) map[string][]string {
	out := map[string][]string{}
	for _, o := range ops {
		for k, v := range unionPathParams(o.path, o.op) {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractPathParams returns ordered path placeholders from the URL template,
// annotated with whether each accepts a list value.
func extractPathParams(pathItem *openapi3.PathItem, op *openapi3.Operation, urlPath string) []pathParam {
	paramSet := make(map[string]bool)
	arraySet := make(map[string]bool)

	for _, p := range pathItem.Parameters {
		if p.Value == nil || p.Value.In != openapi3.ParameterInPath {
			continue
		}
		paramSet[p.Value.Name] = true
		if isArrayParam(p.Value) {
			arraySet[p.Value.Name] = true
		}
	}
	for _, p := range op.Parameters {
		if p.Value == nil || p.Value.In != openapi3.ParameterInPath {
			continue
		}
		paramSet[p.Value.Name] = true
		if isArrayParam(p.Value) {
			arraySet[p.Value.Name] = true
		}
	}

	matches := pathParamRE.FindAllStringSubmatch(urlPath, -1)
	var result []pathParam
	seen := make(map[string]bool)
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		if paramSet[name] {
			result = append(result, pathParam{name: name, isList: arraySet[name]})
		}
	}
	return result
}

// extractQueryParams collects query parameters from both path-level and
// operation-level definitions, deduplicates by wire name, and classifies
// each into its Go type.
func extractQueryParams(pathItem *openapi3.PathItem, op *openapi3.Operation, spec *openapi3.T) []apiQueryParam {
	seen := make(map[string]bool)
	var params []apiQueryParam

	collect := func(refs openapi3.Parameters) {
		for _, ref := range refs {
			p := ref.Value
			if p == nil || p.In != "query" {
				continue
			}
			if isGlobalParam(p.Name) {
				continue
			}
			if seen[p.Name] {
				continue
			}
			seen[p.Name] = true

			qp := apiQueryParam{
				GoName:            pathFieldName(p.Name),
				ParamName:         p.Name,
				Description:       p.Description,
				Required:          p.Required,
				Deprecated:        p.Deprecated,
				VersionAdded:      extensionString(p.Extensions, extVersionAdded),
				VersionDeprecated: extensionString(p.Extensions, extVersionDeprecated),
				DeprecationMsg:    extensionString(p.Extensions, extDeprecationMessage),
			}

			if p.Schema != nil && p.Schema.Value != nil {
				s := p.Schema.Value
				qp.GoType, qp.IsDuration, qp.IsBool, qp.IsList, qp.IsInt = classifyParamSchema(s, ref)
				if s.Default != nil {
					qp.Default = fmt.Sprintf("%v", s.Default)
				}
			} else {
				qp.GoType = "string"
			}

			params = append(params, qp)
		}
	}

	collect(pathItem.Parameters)
	collect(op.Parameters)

	sort.Slice(params, func(i, j int) bool {
		return params[i].ParamName < params[j].ParamName
	})
	return params
}

// extractQueryParamsUnion merges query parameters from all operation variants
// in a group, deduplicating by wire name. This ensures that params available
// on any URL pattern appear in the generated Params struct. It also returns
// breadcrumb-eligible exclusions for params dropped by the version filter,
// keyed as "<group>.<paramName>".
func extractQueryParamsUnion(group string, ops []struct {
	method string
	op     *openapi3.Operation
	path   *openapi3.PathItem
	url    string
}, vrange VersionRange,
) ([]apiQueryParam, []ir.Exclusion) {
	seen := make(map[string]bool)
	excSeen := make(map[string]bool)
	var (
		params     []apiQueryParam
		exclusions []ir.Exclusion
	)

	for _, o := range ops {
		collect := func(refs openapi3.Parameters) {
			for _, ref := range refs {
				p := ref.Value
				if p == nil || p.In != "query" {
					continue
				}
				if isGlobalParam(p.Name) {
					continue
				}
				if seen[p.Name] {
					continue
				}

				vAdded := extensionString(p.Extensions, extVersionAdded)
				vRemoved := extensionString(p.Extensions, extVersionRemoved)
				vDeprecated := extensionString(p.Extensions, extVersionDeprecated)
				if !vrange.Includes(vAdded, vRemoved, vDeprecated) {
					qualified := group + "." + p.Name
					if !excSeen[qualified] {
						excSeen[qualified] = true
						if exc := vrange.Exclusion(qualified, vAdded, vRemoved, vDeprecated); exc != nil {
							exclusions = append(exclusions, ir.Exclusion{
								Name:    exc.Name,
								Reason:  exc.Reason,
								IsOlder: exc.IsOlder,
							})
						}
					}
					continue
				}

				seen[p.Name] = true

				qp := apiQueryParam{
					GoName:            pathFieldName(p.Name),
					ParamName:         p.Name,
					Description:       p.Description,
					Required:          p.Required,
					Deprecated:        p.Deprecated,
					VersionAdded:      vAdded,
					VersionDeprecated: extensionString(p.Extensions, extVersionDeprecated),
					DeprecationMsg:    extensionString(p.Extensions, extDeprecationMessage),
				}

				if p.Schema != nil && p.Schema.Value != nil {
					s := p.Schema.Value
					qp.GoType, qp.IsDuration, qp.IsBool, qp.IsList, qp.IsInt = classifyParamSchema(s, ref)
					if s.Default != nil {
						qp.Default = fmt.Sprintf("%v", s.Default)
					}
				} else {
					qp.GoType = "string"
				}

				params = append(params, qp)
			}
		}

		collect(o.path.Parameters)
		collect(o.op.Parameters)
	}

	sort.Slice(params, func(i, j int) bool {
		return params[i].ParamName < params[j].ParamName
	})
	sort.Slice(exclusions, func(i, j int) bool { return exclusions[i].Name < exclusions[j].Name })
	return params, exclusions
}

// sharedParamGroups maps wire names of query parameters handled by shared
// param group structs (TimeoutParams, DebugParams) to their group. Parameters
// in this map are filtered out of per-operation QueryParams since they're
// provided by embedded structs. Populated by init(); conflicts panic.
var sharedParamGroups map[string]ir.ParamGroup //nolint:gochecknoglobals // init-time registry, immutable after init

func init() { //nolint:gochecknoinits // validates shared param group assignments at startup
	entries := []struct {
		group ir.ParamGroup
		names []string
	}{
		{ir.ParamGroupTimeout, []string{
			"timeout", "cluster_manager_timeout", "master_timeout",
		}},
		{ir.ParamGroupDebug, []string{
			"pretty", "human", "error_trace", "source", "filter_path",
			"format", "help", "v", "s", "h",
		}},
	}

	sharedParamGroups = make(map[string]ir.ParamGroup)
	for _, e := range entries {
		for _, name := range e.names {
			if prev, exists := sharedParamGroups[name]; exists {
				panic(fmt.Sprintf("sharedParamGroups: param %q appears in both %q and %q groups",
					name, prev, e.group))
			}
			sharedParamGroups[name] = e.group
		}
	}
}

// isGlobalParam returns true for query parameters handled by shared param
// group structs (TimeoutParams, DebugParams).
func isGlobalParam(name string) bool {
	_, ok := sharedParamGroups[name]
	return ok
}

// sharedParamGroup returns the ParamGroup for a shared parameter wire name,
// or ParamGroupOperation if the name is not shared.
func sharedParamGroup(name string) ir.ParamGroup {
	if g, ok := sharedParamGroups[name]; ok {
		return g
	}
	return ir.ParamGroupOperation
}

// classifyParamSchema maps an OpenAPI schema to its Go type and type flags.
// It handles duration patterns, booleans, integers, and array types.
func classifyParamSchema(s *openapi3.Schema, paramRef *openapi3.ParameterRef) (goType string, isDuration, isBool, isList, isInt bool) {
	if isDurationSchema(s, paramRef) {
		return "time.Duration", true, false, false, false
	}
	if s.Type != nil {
		if s.Type.Is("boolean") {
			return "bool", false, true, false, false
		}
		if s.Type.Is("integer") {
			return "int", false, false, false, true
		}
		if s.Type.Is("number") {
			return "int", false, false, false, true
		}
		if s.Type.Is("array") {
			return "[]string", false, false, true, false
		}
	}
	if hasOneOfType(s, "array") {
		return "[]string", false, false, true, false
	}
	return "string", false, false, false, false
}

// isDurationSchema detects OpenSearch duration parameters by checking for
// a "nanos" regex pattern or a $ref containing "Duration".
func isDurationSchema(s *openapi3.Schema, paramRef *openapi3.ParameterRef) bool {
	if s.Pattern != "" && strings.Contains(s.Pattern, "nanos") {
		return true
	}
	if paramRef != nil && paramRef.Value != nil && paramRef.Value.Schema != nil {
		ref := paramRef.Value.Schema.Ref
		if strings.Contains(ref, "Duration") {
			return true
		}
	}
	return false
}

// hasOneOfType checks if a schema's oneOf/anyOf alternatives include a given type.
func hasOneOfType(s *openapi3.Schema, typeName string) bool {
	for _, ref := range s.OneOf {
		if ref.Value != nil && ref.Value.Type != nil && ref.Value.Type.Is(typeName) {
			return true
		}
	}
	for _, ref := range s.AnyOf {
		if ref.Value != nil && ref.Value.Type != nil && ref.Value.Type.Is(typeName) {
			return true
		}
	}
	return false
}

// hasRequiredScalarPath returns true if any path field is a scalar (non-list)
// string, meaning the caller must provide a value. List-typed path fields
// ([]string) are always optional since their zero value (nil) produces a
// valid URL pattern without that segment.
func hasRequiredScalarPath(fields []apiPathField) bool {
	for _, f := range fields {
		if !f.IsList {
			return true
		}
	}
	return false
}
