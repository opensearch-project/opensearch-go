// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

// opGroup collects all path variants sharing an x-operation-group value.
type opGroup struct {
	name      string
	pathSpecs []pathVariant
}

// pathVariant is one URL path variant within an operation group.
type pathVariant struct {
	path               string
	pathParams         []string          // ordered parameter names from the URL
	arrayParams        map[string]bool   // params whose spec schema is type=array
	deprecated         bool
	deprecationMessage string
}

// loadAndGroup loads an OpenAPI spec and groups operations by x-operation-group.
func loadAndGroup(specPath string, filter map[string]bool) ([]opGroup, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("loading spec: %w", err)
	}

	return groupFromSpec(spec, filter), nil
}

// groupFromSpec extracts operation groups from a parsed spec.
func groupFromSpec(spec *openapi3.T, filter map[string]bool) []opGroup {
	grouped := make(map[string]*opGroup)

	for urlPath, pathItem := range spec.Paths.Map() {
		for _, op := range pathItem.Operations() {
			group := operationGroup(op)
			if group == "" {
				continue
			}
			if filter != nil && !filter[group] {
				continue
			}

			params, arrayParams := pathParamInfo(pathItem, op, urlPath)

			g, ok := grouped[group]
			if !ok {
				g = &opGroup{name: group}
				grouped[group] = g
			}

			if existing := g.findPath(urlPath); existing != nil {
				if !op.Deprecated {
					existing.deprecated = false
					existing.deprecationMessage = ""
				}
			} else {
				g.pathSpecs = append(g.pathSpecs, pathVariant{
					path:               urlPath,
					pathParams:         params,
					arrayParams:        arrayParams,
					deprecated:         op.Deprecated,
					deprecationMessage: deprecationMessage(op),
				})
			}
		}
	}

	result := make([]opGroup, 0, len(grouped))
	for _, g := range grouped {
		result = append(result, *g)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].name < result[j].name
	})
	return result
}

func (g *opGroup) findPath(path string) *pathVariant {
	for i := range g.pathSpecs {
		if g.pathSpecs[i].path == path {
			return &g.pathSpecs[i]
		}
	}
	return nil
}

// pathParamInfo extracts path parameter names and identifies which are array-typed,
// ordered by their appearance in the URL template.
func pathParamInfo(pathItem *openapi3.PathItem, op *openapi3.Operation, urlPath string) ([]string, map[string]bool) {
	paramSet := make(map[string]bool)
	arraySet := make(map[string]bool)

	for _, p := range pathItem.Parameters {
		if p.Value != nil && p.Value.In == "path" {
			paramSet[p.Value.Name] = true
			if isArrayParam(p.Value) {
				arraySet[p.Value.Name] = true
			}
		}
	}
	for _, p := range op.Parameters {
		if p.Value != nil && p.Value.In == "path" {
			paramSet[p.Value.Name] = true
			if isArrayParam(p.Value) {
				arraySet[p.Value.Name] = true
			}
		}
	}

	// Order by appearance in URL template.
	matches := pathParamRE.FindAllStringSubmatch(urlPath, -1)
	ordered := make([]string, 0, len(matches))
	for _, m := range matches {
		name := m[1]
		if paramSet[name] {
			ordered = append(ordered, name)
		} else {
			ordered = append(ordered, name)
		}
	}
	return ordered, arraySet
}

// isArrayParam returns true if the parameter accepts multiple comma-separated values.
// This covers direct array schemas and oneOf/anyOf unions containing an array variant.
func isArrayParam(p *openapi3.Parameter) bool {
	if p.Schema == nil || p.Schema.Value == nil {
		return false
	}
	return schemaIsArray(p.Schema.Value)
}

func schemaIsArray(s *openapi3.Schema) bool {
	if s.Type != nil && s.Type.Is("array") {
		return true
	}
	for _, ref := range s.OneOf {
		if ref.Value != nil && ref.Value.Type != nil && ref.Value.Type.Is("array") {
			return true
		}
	}
	for _, ref := range s.AnyOf {
		if ref.Value != nil && ref.Value.Type != nil && ref.Value.Type.Is("array") {
			return true
		}
	}
	return false
}

// operationGroup reads x-operation-group from an operation's extensions.
func operationGroup(op *openapi3.Operation) string {
	if op == nil || op.Extensions == nil {
		return ""
	}
	ext, ok := op.Extensions["x-operation-group"]
	if !ok {
		return ""
	}
	switch v := ext.(type) {
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return ""
		}
		return s
	case string:
		return v
	default:
		return ""
	}
}

// deprecationMessage reads x-deprecation-message from an operation's extensions.
func deprecationMessage(op *openapi3.Operation) string {
	if op == nil || op.Extensions == nil {
		return ""
	}
	ext, ok := op.Extensions["x-deprecation-message"]
	if !ok {
		return ""
	}
	switch v := ext.(type) {
	case json.RawMessage:
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return ""
		}
		return s
	case string:
		return v
	default:
		return ""
	}
}
