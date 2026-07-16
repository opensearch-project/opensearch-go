// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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
	// Resolve inline object branch names up front: naming is content-based and
	// collision detection needs the whole branch set (see objectBranchNames).
	objNames := objectBranchNames(branches)
	for _, branch := range branches {
		if branch == nil {
			continue
		}
		// Skip null branches (handled by pointer semantics).
		if branch.Value != nil && branch.Value.Type != nil && branch.Value.Type.Is("null") {
			continue
		}
		b := w.classifyBranch(branch, schemaKey, group, branchIdx, objNames[branchIdx])
		if b.GoType == "" {
			branchIdx++
			continue
		}
		// branchIdx is the spec-array position; record it as the branch's
		// order source of truth so no downstream sort has to parse the Name.
		b.Ordinal = branchIdx
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

	// Collapse branches that are indistinguishable when decoded from the same
	// JSON token (e.g. int/int32/int64, or float32/float64): a try-each decoder
	// can only ever reach the first, so the narrower siblings are dead and risk
	// silent truncation. This can drop the union back to a single branch.
	classified = collapseEquivalentBranches(classified)
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
// branchIdx is the branch's position among non-null branches (its Ordinal).
// objName is the content-based name resolved for an inline object branch (see
// objectBranchNames); it is "" for non-object branches and for object branches
// whose content name collided with a sibling.
func (w *walker) classifyBranch(ref *openapi3.SchemaRef, parentKey, group string, branchIdx int, objName string) unionBranch {
	if ref == nil {
		return unionBranch{}
	}

	if ref.Ref != "" {
		return w.classifyRefBranch(ref, parentKey, group)
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
		return w.classifyObjectBranch(s, parentKey, group, branchIdx, versionAdded, objName)
	}

	return unionBranch{}
}

// classifyObjectBranch resolves an inline object oneOf/anyOf branch. An object
// with properties becomes a named type; an open object (additionalProperties
// only) falls back to a raw map branch. name is the branch's resolved suffix,
// computed by the caller from branch content (see objectBranchName); the caller
// passes "" when content naming collided with a sibling, in which case the
// branch falls back to a positional Object<idx> suffix so the two remain
// distinct types.
func (w *walker) classifyObjectBranch(s *openapi3.Schema, parentKey, group string, branchIdx int, versionAdded, name string) unionBranch {
	// Open object (additionalProperties) with no declared properties.
	if len(s.Properties) == 0 {
		return unionBranch{
			Name:         "Map",
			GoType:       "map[string]json.RawMessage",
			TokenClass:   "object",
			VersionAdded: versionAdded,
		}
	}

	// The branch name doubles as the registry key suffix and the generated type
	// suffix, so accessors and constructors read semantically without stuttering
	// the union prefix (e.g. NewFooFromTask, not NewFooFromFooObject1). name is
	// empty only when content naming collided with a sibling; fall back to the
	// positional suffix, which keeps colliding branches as distinct types.
	if name == "" {
		name = fmt.Sprintf("Object%d", branchIdx)
	}
	childKey := fmt.Sprintf("%s.%s", parentKey, name)
	goTypeName := w.resolveObjectSchema(s, childKey, group, false)
	if goTypeName != "" && goTypeName != "json.RawMessage" {
		return unionBranch{
			Name:         name,
			GoType:       goTypeName,
			TokenClass:   "object",
			Required:     flattenRequired(s),
			IsRef:        true,
			VersionAdded: versionAdded,
		}
	}

	// Properties present but unresolvable to a named type: raw map fallback.
	return unionBranch{
		Name:         "Map",
		GoType:       "map[string]json.RawMessage",
		TokenClass:   "object",
		VersionAdded: versionAdded,
	}
}

// objectBranchName derives an inline object branch's name from its content, so
// the generated type is stable when the spec reorders oneOf/anyOf members. A
// titled member uses its title. Otherwise a branch that declares required keys
// is named for its first (sorted) required key -- the field a decoder probes to
// select it -- and a permissive branch (no required keys) is named for its
// sorted property keys joined together. Every fragment runs through baseGoName
// so JSON keys become valid identifier fragments (e.g. "_source" -> "Source").
// Returns "" for an object with no properties (an open map branch, named
// elsewhere).
func objectBranchName(s *openapi3.Schema) string {
	if s.Title != "" {
		// baseGoName splits on '-', '_', '.' so a hyphenated title
		// (e.g. "score-ranker-processor") normalizes to ScoreRankerProcessor.
		return baseGoName(s.Title)
	}
	if len(s.Properties) == 0 {
		return ""
	}
	if req := flattenRequired(s); len(req) > 0 {
		sorted := slices.Clone(req)
		sort.Strings(sorted)
		return baseGoName(sorted[0])
	}
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(baseGoName(k))
	}
	return sb.String()
}

// objectBranchNames resolves the content-based name of every inline object
// branch in a oneOf/anyOf, keyed by the branch's Ordinal (spec-array position
// among non-null branches, matching resolveUnionType's branchIdx). Names shared
// by more than one branch are dropped to "" so those siblings fall back to
// distinct positional suffixes: two structurally identical branches (same
// properties and required set) cannot be told apart by content, so collapsing
// them to one type would silently drop a union branch.
func objectBranchNames(branches []*openapi3.SchemaRef) map[int]string {
	names := map[int]string{}
	idx := 0
	for _, br := range branches {
		if br == nil {
			continue
		}
		if br.Value != nil && br.Value.Type != nil && br.Value.Type.Is("null") {
			continue
		}
		if br.Ref == "" && br.Value != nil && br.Value.Type != nil && br.Value.Type.Is("object") {
			if n := objectBranchName(br.Value); n != "" {
				names[idx] = n
			}
		}
		idx++
	}
	counts := map[string]int{}
	for _, n := range names {
		counts[n]++
	}
	for idx, n := range names {
		if counts[n] > 1 {
			delete(names, idx) // collision: fall back to positional Object<idx>
		}
	}
	return names
}

// classifyRefBranch resolves a $ref-bearing union branch into its unionBranch.
// Handles aliases (scalar primitives), primitive results from named schemas,
// map and slice composite types, and named object refs.
func (w *walker) classifyRefBranch(ref *openapi3.SchemaRef, parentKey, group string) unionBranch {
	key := refToSchemaKey(ref.Ref)
	if goType, ok := isScalarAlias(key); ok {
		return unionBranch{
			Name:       primitiveBranchName(goType),
			GoType:     goType,
			TokenClass: tokenClassForPrimitive(goType),
		}
	}

	goTypeName := w.walkSchema(ref, parentKey, group, false)
	if goTypeName == "" || goTypeName == "json.RawMessage" {
		return unionBranch{}
	}

	// The x-version-added extension lives on the resolved (referenced) schema.
	// Without it, sortBranchesNewestFirst orders $ref branches as if unversioned.
	var versionAdded string
	if ref.Value != nil {
		versionAdded = extensionString(ref.Value.Extensions, extVersionAdded)
	}

	if isPrimitiveType(goTypeName) {
		return unionBranch{
			Name:         primitiveBranchName(goTypeName),
			GoType:       goTypeName,
			TokenClass:   tokenClassForPrimitive(goTypeName),
			VersionAdded: versionAdded,
		}
	}

	if strings.HasPrefix(goTypeName, "map[") {
		return unionBranch{Name: "Map", GoType: goTypeName, TokenClass: "object", VersionAdded: versionAdded}
	}
	if strings.HasPrefix(goTypeName, "[]") {
		return unionBranch{Name: "Array", GoType: goTypeName, TokenClass: "array", VersionAdded: versionAdded}
	}

	branchName := deriveBranchName(ref, goTypeName)
	required := flattenRequired(ref.Value)

	return unionBranch{
		Name:         branchName,
		GoType:       goTypeName,
		TokenClass:   tokenClassForSchemaValue(ref.Value),
		Required:     required,
		IsRef:        true,
		VersionAdded: versionAdded,
	}
}

// flattenRequired returns the property names a schema requires, including those
// contributed by its allOf members (recursively). The OpenAPI bundle does not
// merge allOf, so a schema that extends a base via allOf and marks a new field
// required (e.g. NodeReloadError adding required reload_exception) carries that
// requirement on an allOf member rather than at the root. Union discrimination
// needs the full set to find a branch's distinguishing keys.
func flattenRequired(s *openapi3.Schema) []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	var walk func(*openapi3.Schema)
	walk = func(sch *openapi3.Schema) {
		if sch == nil {
			return
		}
		for _, k := range sch.Required {
			if _, ok := seen[k]; !ok {
				seen[k] = struct{}{}
				out = append(out, k)
			}
		}
		for _, sub := range sch.AllOf {
			if sub != nil {
				walk(sub.Value)
			}
		}
	}
	walk(s)
	return out
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

// decodeEquivalentGroups lists Go primitive types that decode from the same
// JSON token and are therefore indistinguishable in a try-each union: only the
// first such branch attempted is ever reachable. Each group is ordered widest-
// first; when a union declares more than one member of a group, only the
// widest (first-listed) survives so the kept accessor never truncates a value
// the dropped branches could have held. Integer and float groups stay separate
// because a float branch accepts integers but an int branch rejects decimals,
// so the two classes remain mutually reachable. string and bool need no group:
// each has a single Go type, and exact GoType duplicates are already removed by
// deduplicateBranches.
//
//nolint:gochecknoglobals // static lookup table; package-level so it's visible next to its doc comment and the funcs that consult it
var decodeEquivalentGroups = [][]string{
	{"int64", "int", "int32"},
	{"float64", "float32"},
}

// collapseEquivalentBranches drops branches that are decode-indistinguishable
// from a wider sibling (see decodeEquivalentGroups), keeping each group's
// widest member in its original position. Collapsing can reduce a union back to
// a single branch.
func collapseEquivalentBranches(branches []unionBranch) []unionBranch {
	drop := make(map[int]struct{})
	for _, group := range decodeEquivalentGroups {
		best, bestRank := -1, len(group)
		for i := range branches {
			rank := slices.Index(group, branches[i].GoType)
			if rank >= 0 && rank < bestRank {
				best, bestRank = i, rank
			}
		}
		if best < 0 {
			continue
		}
		for i := range branches {
			if i != best && slices.Index(group, branches[i].GoType) >= 0 {
				drop[i] = struct{}{}
			}
		}
	}
	if len(drop) == 0 {
		return branches
	}
	result := make([]unionBranch, 0, len(branches)-len(drop))
	for i := range branches {
		if _, dropped := drop[i]; !dropped {
			result = append(result, branches[i])
		}
	}
	return result
}

// sortBranchesNewestFirst reorders branches so that those with higher
// x-version-added values appear first. Branches without version info
// are placed after versioned branches, and ties break on spec-array
// order (Ordinal) so the result is independent of the incoming slice
// order. This ensures try-each unmarshal attempts the newest schema first.
func sortBranchesNewestFirst(branches []unionBranch) {
	sort.Slice(branches, func(i, j int) bool {
		vi, vj := branches[i].VersionAdded, branches[j].VersionAdded
		if vi == vj {
			return branches[i].Ordinal < branches[j].Ordinal
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
