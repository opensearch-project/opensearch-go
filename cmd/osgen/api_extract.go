// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// apiOperation holds everything needed to generate one set of API files.
type apiOperation struct {
	Group             string
	TypePrefix        string
	PathBuilderName   string
	HTTPMethod        string
	HTTPVerb          string // human-readable verb (e.g. "POST") for doc comments
	PrimaryPath       string // canonical URL path pattern (e.g. "/{index}/_refresh")
	Description       string
	VersionAdded      string
	VersionDeprecated string
	Deprecated        bool
	DeprecationMsg    string
	DocsURL           string
	ExcludedDistros   []string
	HasBody           bool
	PathFields        []apiPathField
	QueryParams       []apiQueryParam
}

// apiPathField is one path parameter exposed as a struct field on the Req.
// The generated Req struct includes one field per URL path placeholder.
type apiPathField struct {
	GoName string // exported Go field name (e.g. "IndexUUID")
	IsList bool   // true if the parameter accepts comma-separated values
}

// apiQueryParam is one query parameter exposed on the Params struct.
// Each field maps to a URL query key in the OpenSearch REST API.
type apiQueryParam struct {
	GoName            string // exported Go field name
	ParamName         string // wire name used in the query string (e.g. "wait_for_active_shards")
	GoType            string // Go type for the field (e.g. "string", "bool", "time.Duration")
	IsDuration        bool   // true if the value encodes as an OpenSearch duration string
	IsBool            bool
	IsList            bool // true if the value is comma-joined ([]string)
	IsInt             bool
	Deprecated        bool
	VersionDeprecated string // semver when this param was deprecated
}

// extractOperations parses the OpenAPI spec and returns one apiOperation per
// x-operation-group. An optional filter restricts output to named groups.
func extractOperations(specPath string, filter map[string]bool) ([]apiOperation, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("loading spec: %w", err)
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

	for urlPath, pathItem := range spec.Paths.Map() {
		for method, op := range pathItem.Operations() {
			group := operationGroup(op)
			if group == "" {
				continue
			}
			if extensionBool(op.Extensions, extIgnorable) {
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
	for group, gd := range grouped {
		op := buildAPIOperation(group, gd.ops, spec)
		result = append(result, op)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Group < result[j].Group
	})
	return result, nil
}

// buildAPIOperation constructs an apiOperation from all path variants sharing
// a group name. It picks the primary (non-deprecated) variant for metadata and
// merges query parameters from all variants.
func buildAPIOperation(group string, ops []struct {
	method string
	op     *openapi3.Operation
	path   *openapi3.PathItem
	url    string
}, spec *openapi3.T) apiOperation {
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

	apiOp := apiOperation{
		Group:             group,
		TypePrefix:        typePrefix,
		PathBuilderName:   pathBuilderName(group),
		HTTPMethod:        httpMethodConst(primary.method),
		HTTPVerb:          strings.ToUpper(primary.method),
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

	// Extract path fields from the path builder.
	pathParams := extractPathParams(primary.path, op, primary.url)
	for _, pp := range pathParams {
		apiOp.PathFields = append(apiOp.PathFields, apiPathField{
			GoName: pathFieldName(pp.name),
			IsList: pp.isList,
		})
	}

	// Extract query parameters.
	apiOp.QueryParams = extractQueryParams(primary.path, op, spec)

	return apiOp
}

type pathParam struct {
	name   string
	isList bool
}

// extractPathParams returns ordered path placeholders from the URL template,
// annotated with whether each accepts a list value.
func extractPathParams(pathItem *openapi3.PathItem, op *openapi3.Operation, urlPath string) []pathParam {
	paramSet := make(map[string]bool)
	arraySet := make(map[string]bool)

	for _, p := range pathItem.Parameters {
		if p.Value == nil || p.Value.In != "path" {
			continue
		}
		paramSet[p.Value.Name] = true
		if isArrayParam(p.Value) {
			arraySet[p.Value.Name] = true
		}
	}
	for _, p := range op.Parameters {
		if p.Value == nil || p.Value.In != "path" {
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
				Deprecated:        p.Deprecated,
				VersionDeprecated: extensionString(p.Extensions, extVersionDeprecated),
			}

			if p.Schema != nil && p.Schema.Value != nil {
				s := p.Schema.Value
				qp.GoType, qp.IsDuration, qp.IsBool, qp.IsList, qp.IsInt = classifyParamSchema(s, ref)
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

// isGlobalParam returns true for query parameters that are handled globally
// by the client (pretty-printing, error trace, etc.) and should not appear
// as per-operation Params fields.
func isGlobalParam(name string) bool {
	switch name {
	case "pretty", "human", "error_trace", "source", "filter_path":
		return true
	}
	return false
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
