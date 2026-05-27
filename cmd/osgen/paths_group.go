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

// opGroup collects all path variants sharing an x-operation-group value.
type opGroup struct {
	name      string
	pathSpecs []pathVariant
	// unionParams maps a synthetic path-parameter name (e.g.
	// "node_id_or_metric") to the list of member parameter names its
	// schema's anyOf resolves to (e.g. ["node_id", "metric"]). Populated
	// from path parameters whose schema is an anyOf where every member
	// title matches another path parameter name within the same group.
	// Used to expand union-typed single-segment paths into one synthetic
	// path variant per member, eliminating the need for a synthetic
	// struct field and producing the correct switch-style Build().
	unionParams map[string][]string
}

// pathVariant is one URL path variant within an operation group.
type pathVariant struct {
	path               string
	methods            map[string]struct{} // HTTP methods available at this path (e.g. "GET", "POST")
	pathParams         []string
	arrayParams        map[string]bool
	deprecated         bool
	deprecationMessage string
	description        string
	versionAdded       string
	versionDeprecated  string
	versionRemoved     string
	docsURL            string
	excludedDistros    []string
}

// loadAndGroup loads an OpenAPI spec and groups operations by x-operation-group.
func loadAndGroup(specPath string, filter map[string]bool, vrange VersionRange) ([]opGroup, []ExclusionReason, error) {
	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = true

	spec, err := loader.LoadFromFile(specPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading spec: %w", err)
	}

	groups, excluded := groupFromSpec(spec, filter, vrange)
	return groups, excluded, nil
}

// groupFromSpec extracts operation groups from a parsed spec.
func groupFromSpec(spec *openapi3.T, filter map[string]bool, vrange VersionRange) ([]opGroup, []ExclusionReason) {
	grouped := make(map[string]*opGroup)
	var excluded []ExclusionReason

	// Iterate paths and methods in sorted order so that when a single URL
	// serves multiple HTTP methods, the metadata captured from the first
	// op (description, versionAdded, docsURL) is deterministic instead of
	// depending on Go's randomized map iteration.
	pathKeys := make([]string, 0, len(spec.Paths.Map()))
	for k := range spec.Paths.Map() {
		pathKeys = append(pathKeys, k)
	}
	sort.Strings(pathKeys)

	for _, urlPath := range pathKeys {
		pathItem := spec.Paths.Map()[urlPath]
		ops := pathItem.Operations()
		methods := make([]string, 0, len(ops))
		for m := range ops {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, method := range methods {
			op := ops[method]
			if extensionBool(op.Extensions, extIgnorable) {
				continue
			}

			group := operationGroup(op)
			if group == "" {
				continue
			}
			if filter != nil && !filter[group] {
				continue
			}

			vAdded := extensionString(op.Extensions, extVersionAdded)
			vRemoved := extensionString(op.Extensions, extVersionRemoved)
			vDeprecated := extensionString(op.Extensions, extVersionDeprecated)
			if !vrange.Includes(vAdded, vRemoved, vDeprecated) {
				if exc := vrange.Exclusion(group, vAdded, vRemoved, vDeprecated); exc != nil {
					excluded = append(excluded, *exc)
				}
				continue
			}

			params, arrayParams := pathParamInfo(pathItem, op, urlPath)

			g, ok := grouped[group]
			if !ok {
				g = &opGroup{name: group}
				grouped[group] = g
			}

			for unionName, members := range unionPathParams(pathItem, op) {
				if g.unionParams == nil {
					g.unionParams = make(map[string][]string)
				}
				g.unionParams[unionName] = members
			}

			httpMethod := strings.ToUpper(method)

			if existing := g.findPath(urlPath); existing != nil {
				if !op.Deprecated {
					existing.deprecated = false
					existing.deprecationMessage = ""
				}
				existing.methods[httpMethod] = struct{}{}
				continue
			}

			var docsURL string
			if op.ExternalDocs != nil {
				docsURL = op.ExternalDocs.URL
			}

			g.pathSpecs = append(g.pathSpecs, pathVariant{
				path:               urlPath,
				methods:            map[string]struct{}{httpMethod: {}},
				pathParams:         params,
				arrayParams:        arrayParams,
				deprecated:         op.Deprecated,
				deprecationMessage: deprecationMessage(op),
				description:        op.Description,
				versionAdded:       vAdded,
				versionDeprecated:  extensionString(op.Extensions, extVersionDeprecated),
				versionRemoved:     vRemoved,
				docsURL:            docsURL,
				excludedDistros:    extensionStringSlice(op.Extensions, extDistributionsExcluded),
			})
		}
	}

	result := make([]opGroup, 0, len(grouped))
	for _, g := range grouped {
		sort.Slice(g.pathSpecs, func(i, j int) bool {
			return g.pathSpecs[i].path < g.pathSpecs[j].path
		})
		result = append(result, *g)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].name < result[j].name
	})
	return result, excluded
}

func (g *opGroup) findPath(path string) *pathVariant {
	for i := range g.pathSpecs {
		if g.pathSpecs[i].path == path {
			return &g.pathSpecs[i]
		}
	}
	return nil
}

// pathParamInfo extracts path parameter names and identifies which are array-typed.
func pathParamInfo(pathItem *openapi3.PathItem, op *openapi3.Operation, urlPath string) ([]string, map[string]bool) {
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
	ordered := make([]string, 0, len(matches))
	for _, m := range matches {
		ordered = append(ordered, m[1])
	}
	return ordered, arraySet
}

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

// unionPathParams returns a map of path-parameter name to anyOf member
// titles for parameters whose schema declares the segment as a union of
// other named parameters (e.g. nodes.info's "node_id_or_metric" =
// anyOf({title: node_id, ...}, {title: metric, ...})). Member titles are
// validated as group-level path parameter names by expandUnionPaths.
func unionPathParams(pathItem *openapi3.PathItem, op *openapi3.Operation) map[string][]string {
	out := map[string][]string{}
	collect := func(p *openapi3.Parameter) {
		if p == nil || p.In != openapi3.ParameterInPath || p.Schema == nil || p.Schema.Value == nil {
			return
		}
		members := schemaUnionMembers(p.Schema.Value)
		if len(members) == 0 {
			return
		}
		out[p.Name] = members
	}
	for _, p := range pathItem.Parameters {
		collect(p.Value)
	}
	for _, p := range op.Parameters {
		collect(p.Value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// schemaUnionMembers returns the titles of every member in s.AnyOf when
// the schema has at least two members and every member carries a Title.
// Returns nil otherwise so the caller treats the parameter as a normal
// (non-union) segment.
func schemaUnionMembers(s *openapi3.Schema) []string {
	if len(s.AnyOf) < 2 {
		return nil
	}
	members := make([]string, 0, len(s.AnyOf))
	for _, ref := range s.AnyOf {
		if ref.Value == nil || ref.Value.Title == "" {
			return nil
		}
		members = append(members, ref.Value.Title)
	}
	return members
}

// expandUnionPaths replaces each path variant containing a union path
// parameter with one synthetic variant per anyOf member, preserving all
// other metadata. The expansion only fires when every member name also
// appears as a path parameter in some other variant of the same group --
// otherwise the union has no concrete realization and the synthetic
// param is left in place.
//
// Example: nodes.info has variants /_nodes, /_nodes/{node_id_or_metric},
// /_nodes/{node_id}/{metric}. The two-segment variant defines node_id
// and metric as real path parameters, so the union expands to
// /_nodes/{node_id} and /_nodes/{metric}. The downstream trie/emit
// passes then produce a switch over (NodeID, Metric) without ever
// modeling node_id_or_metric as a struct field.
func expandUnionPaths(g *opGroup) {
	if len(g.unionParams) == 0 {
		return
	}

	knownParams := make(map[string]bool)
	for _, pv := range g.pathSpecs {
		for _, p := range pv.pathParams {
			knownParams[p] = true
		}
	}

	usable := make(map[string][]string, len(g.unionParams))
	for unionName, members := range g.unionParams {
		ok := true
		for _, m := range members {
			if !knownParams[m] {
				ok = false
				break
			}
		}
		if ok {
			usable[unionName] = members
		}
	}
	if len(usable) == 0 {
		return
	}

	existing := make(map[string]bool, len(g.pathSpecs))
	for _, pv := range g.pathSpecs {
		existing[pv.path] = true
	}

	expanded := make([]pathVariant, 0, len(g.pathSpecs))
	for _, pv := range g.pathSpecs {
		var unionName string
		for _, p := range pv.pathParams {
			if _, ok := usable[p]; ok {
				unionName = p
				break
			}
		}
		if unionName == "" {
			expanded = append(expanded, pv)
			continue
		}

		members := usable[unionName]
		for _, m := range members {
			synPath := strings.Replace(pv.path, "{"+unionName+"}", "{"+m+"}", 1)
			if existing[synPath] {
				continue
			}
			existing[synPath] = true

			syn := pv
			syn.path = synPath
			syn.pathParams = make([]string, len(pv.pathParams))
			copy(syn.pathParams, pv.pathParams)
			for i, p := range syn.pathParams {
				if p == unionName {
					syn.pathParams[i] = m
					break
				}
			}
			syn.arrayParams = make(map[string]bool, len(pv.arrayParams))
			for k, v := range pv.arrayParams {
				if k == unionName {
					syn.arrayParams[m] = v
				} else {
					syn.arrayParams[k] = v
				}
			}
			syn.methods = make(map[string]struct{}, len(pv.methods))
			for k, v := range pv.methods {
				syn.methods[k] = v
			}
			expanded = append(expanded, syn)
		}
	}
	g.pathSpecs = expanded
}
