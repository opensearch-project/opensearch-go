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
	"unicode"

	"github.com/getkin/kin-openapi/openapi3"
	"golang.org/x/mod/semver"
)

// resolveUnionType classifies a oneOf/anyOf schema into branches, registers
// a discriminated union goType in the registry, and returns the Go type name.
// Returns "json.RawMessage" only if the schema cannot be meaningfully resolved
// (e.g., single null branch, no valid branches).
func (w *walker) resolveUnionType(schema *openapi3.Schema, schemaKey, group string) string {
	branches := schema.OneOf
	if len(branches) == 0 {
		branches = schema.AnyOf
	}

	var classified []unionBranch
	branchIdx := 0
	for _, branch := range branches {
		if branch == nil {
			continue
		}
		// Skip null branches (handled by pointer semantics).
		if branch.Value != nil && branch.Value.Type != nil && branch.Value.Type.Is("null") {
			continue
		}
		b := w.classifyBranch(branch, schemaKey, group, branchIdx)
		if b.GoType == "" {
			branchIdx++
			continue
		}
		classified = append(classified, b)
		branchIdx++
	}

	if len(classified) < 2 {
		// Single non-null branch or no branches: not a union.
		if len(classified) == 1 {
			return classified[0].GoType
		}
		return "json.RawMessage"
	}

	// Deduplicate branches by GoType (some specs list the same type twice).
	classified = deduplicateBranches(classified)
	if len(classified) < 2 {
		return classified[0].GoType
	}

	// Disambiguate branches that share the same accessor Name.
	deduplicateAccessorNames(classified)

	// For try-each unions, sort branches newest-first so the most recent
	// (and most likely) version is attempted first during unmarshal.
	if unionNeedsTryEach(classified) {
		sortBranchesNewestFirst(classified)
	}

	name := schemaTypeName(schemaKey, false)
	shared := isSharedSchema(schemaKey)

	ownerGroup := group
	if g := schemaGroup(schemaKey); g != "" {
		ownerGroup = g
	}

	t := &goType{
		Name:      name,
		Pkg:       typePkg(shared, ownerGroup, w.registry),
		SchemaRef: schemaKey,
		IsShared:  shared,
		IsUnion:   true,
		IsLazy:    unionNeedsTryEach(classified),
		Branches:  classified,
		Comment:   schema.Description,
	}

	if registered, ok := w.registry.register(t); ok {
		return registered.Name
	}
	if existing, ok := w.registry.lookup(schemaKey); ok {
		return existing.Name
	}
	return name
}

// classifyBranch resolves a single oneOf/anyOf branch into a unionBranch.
// branchIdx disambiguates inline objects that would otherwise share a registry key.
func (w *walker) classifyBranch(ref *openapi3.SchemaRef, parentKey, group string, branchIdx int) unionBranch {
	if ref == nil {
		return unionBranch{}
	}

	// Named $ref branch.
	if ref.Ref != "" {
		key := refToSchemaKey(ref.Ref)
		if goType, ok := isScalarAlias(key); ok {
			return unionBranch{
				Name:       primitiveBranchName(goType),
				GoType:     goType,
				TokenClass: tokenClassForPrimitive(goType),
			}
		}

		// Walk the referenced schema to register it.
		goTypeName := w.walkSchema(ref, parentKey, group, false)
		if goTypeName == "" || goTypeName == "json.RawMessage" {
			return unionBranch{}
		}

		// If the $ref resolved to a primitive type (e.g., a named string schema),
		// treat it as a primitive branch with proper exported naming.
		if isPrimitiveType(goTypeName) {
			return unionBranch{
				Name:       primitiveBranchName(goTypeName),
				GoType:     goTypeName,
				TokenClass: tokenClassForPrimitive(goTypeName),
			}
		}

		// If the $ref resolved to a map or slice type (from a named schema
		// with additionalProperties or type:array), use fixed field names.
		if strings.HasPrefix(goTypeName, "map[") {
			return unionBranch{
				Name:       "Map",
				GoType:     goTypeName,
				TokenClass: "object",
			}
		}
		if strings.HasPrefix(goTypeName, "[]") {
			return unionBranch{
				Name:       "Array",
				GoType:     goTypeName,
				TokenClass: "array",
			}
		}

		branchName := deriveBranchName(ref, goTypeName)
		var required []string
		if ref.Value != nil {
			required = ref.Value.Required
		}

		return unionBranch{
			Name:       branchName,
			GoType:     goTypeName,
			TokenClass: tokenClassForSchemaValue(ref.Value),
			Required:   required,
			IsRef:      true,
		}
	}

	// Inline branch.
	if ref.Value == nil {
		return unionBranch{}
	}
	s := ref.Value
	versionAdded := extensionString(s.Extensions, extVersionAdded)

	if s.Type == nil {
		return unionBranch{}
	}

	goType := primitiveGoType(s)
	if goType != "" {
		return unionBranch{
			Name:         primitiveBranchName(goType),
			GoType:       goType,
			TokenClass:   tokenClassForPrimitive(goType),
			VersionAdded: versionAdded,
		}
	}

	if s.Type.Is("array") {
		elemType := "json.RawMessage"
		if s.Items != nil {
			elemType = w.walkSchema(s.Items, parentKey+"Item", group, false)
		}
		sliceType := "[]" + elemType
		return unionBranch{
			Name:         "Array",
			GoType:       sliceType,
			TokenClass:   "array",
			VersionAdded: versionAdded,
		}
	}

	if s.Type.Is("object") {
		// Inline object with properties: walk it to register a named type.
		if len(s.Properties) > 0 {
			childKey := fmt.Sprintf("%s.object%d", parentKey, branchIdx)
			goTypeName := w.resolveObjectSchema(s, childKey, group, false)
			if goTypeName != "" && goTypeName != "json.RawMessage" {
				branchName := baseGoName(goTypeName)
				return unionBranch{
					Name:         branchName,
					GoType:       goTypeName,
					TokenClass:   "object",
					Required:     s.Required,
					IsRef:        true,
					VersionAdded: versionAdded,
				}
			}
		}
		// Open object (additionalProperties).
		return unionBranch{
			Name:         "Map",
			GoType:       "map[string]json.RawMessage",
			TokenClass:   "object",
			VersionAdded: versionAdded,
		}
	}

	return unionBranch{}
}

// tokenClassForSchemaValue returns the JSON token class for a resolved schema.
func tokenClassForSchemaValue(schema *openapi3.Schema) string {
	if schema == nil {
		return "object"
	}
	if schema.Type == nil {
		if len(schema.Properties) > 0 || len(schema.AllOf) > 0 {
			return "object"
		}
		return "object"
	}
	switch {
	case schema.Type.Is("object"):
		return "object"
	case schema.Type.Is("array"):
		return "array"
	case schema.Type.Is("string"):
		return "string"
	case schema.Type.Is("integer"), schema.Type.Is("number"):
		return "number"
	case schema.Type.Is("boolean"):
		return "bool"
	}
	return "object"
}

// tokenClassForPrimitive maps a Go type name to its JSON token class.
func tokenClassForPrimitive(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "bool":
		return "bool"
	case "int", "int32", "int64", "float32", "float64":
		return "number"
	}
	if strings.HasPrefix(goType, "[]") {
		return "array"
	}
	if strings.HasPrefix(goType, "map[") {
		return "object"
	}
	return "object"
}

// primitiveBranchName returns the exported Go name for a primitive type
// used as a union branch constant/accessor suffix.
func primitiveBranchName(goType string) string {
	switch goType {
	case "string":
		return "String"
	case "bool":
		return "Bool"
	case "int":
		return "Int"
	case "int32":
		return "Int32"
	case "int64":
		return "Int64"
	case "float32":
		return "Float32"
	case "float64":
		return "Float64"
	}
	return baseGoName(goType)
}

// deduplicateAccessorNames renames branches that share the same Name.
// For example, two map branches both named "Map" become "StringMap" and
// "FieldSortMap" based on their value type.
func deduplicateAccessorNames(branches []unionBranch) {
	count := make(map[string]int, len(branches))
	for _, b := range branches {
		count[b.Name]++
	}
	for i := range branches {
		if count[branches[i].Name] > 1 {
			branches[i].Name = mapValueTypeName(branches[i].GoType) + branches[i].Name
		}
	}
}

// mapValueTypeName extracts a disambiguating prefix from a Go type.
// Handles nested maps and slices recursively, pointer prefixes, and
// arbitrary base types.
//
//	"map[string]FieldSort"           -> "FieldSort"
//	"map[string]string"              -> "String"
//	"[]int"                          -> "Int"
//	"map[string]map[string]FieldSort"-> "FieldSort"
//	"*FieldSort"                     -> "FieldSort"
//	"[]*FieldSort"                   -> "FieldSort"
func mapValueTypeName(goType string) string {
	for {
		switch {
		case strings.HasPrefix(goType, "map["):
			// Find the matching ']' for the key. The key is always
			// "string" in our schemas, so the first ']' after "map["
			// closes the key bracket.
			idx := strings.Index(goType, "]")
			if idx < 0 || idx+1 >= len(goType) {
				return baseGoName(goType)
			}
			goType = goType[idx+1:]
		case strings.HasPrefix(goType, "[]"):
			goType = goType[2:]
		case strings.HasPrefix(goType, "*"):
			goType = goType[1:]
		default:
			return baseGoName(goType)
		}
	}
}

// deriveBranchName extracts the branch name from a $ref or spec title.
// The fallback to goTypeName runs through baseGoName so cross-package
// type strings ("subpkg.Foo") or hyphenated names yield valid Go
// identifier fragments.
func deriveBranchName(ref *openapi3.SchemaRef, goTypeName string) string {
	// Prefer the spec title if available.
	if ref.Value != nil && ref.Value.Title != "" {
		return baseGoName(ref.Value.Title)
	}
	// Normalize the Go type name through baseGoName to strip dotted
	// package qualifiers and other non-identifier punctuation.
	return baseGoName(goTypeName)
}

// isPrimitiveType returns true if the Go type name is a builtin primitive.
func isPrimitiveType(goType string) bool {
	switch goType {
	case "string", "bool", "int", "int32", "int64", "float32", "float64":
		return true
	}
	return false
}

// lcFirst returns s with the first character lowercased.
func lcFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}

// deduplicateBranches removes branches with duplicate GoType values,
// keeping the first occurrence.
func deduplicateBranches(branches []unionBranch) []unionBranch {
	seen := make(map[string]bool, len(branches))
	result := make([]unionBranch, 0, len(branches))
	for _, b := range branches {
		if seen[b.GoType] {
			continue
		}
		seen[b.GoType] = true
		result = append(result, b)
	}
	return result
}

// sortBranchesNewestFirst reorders branches so that those with higher
// x-version-added values appear first. Branches without version info
// are placed after versioned branches in their original relative order.
// This ensures try-each unmarshal attempts the newest schema first.
func sortBranchesNewestFirst(branches []unionBranch) {
	sort.SliceStable(branches, func(i, j int) bool {
		vi, vj := branches[i].VersionAdded, branches[j].VersionAdded
		if vi == "" && vj == "" {
			return false
		}
		if vi == "" {
			return false
		}
		if vj == "" {
			return true
		}
		return semver.Compare("v"+vi, "v"+vj) > 0
	})
}
