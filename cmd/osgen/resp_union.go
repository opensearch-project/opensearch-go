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

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// resolveUnionType classifies a oneOf/anyOf schema into branches, registers a
// union goType in the registry, and returns the Go type name. Returns
// "json.RawMessage" only if the schema cannot be meaningfully resolved (e.g.,
// single null branch, no valid branches).
func (w *walker) resolveUnionType(schema *openapi3.Schema, schemaKey, group string) string {
	branches := schema.OneOf
	if len(branches) == 0 {
		branches = schema.AnyOf
	}

	// Resolve the spec's discriminator, if any, before the branch walk mutates
	// the set: discriminatorValues keys its result by position among non-null
	// spec branches, which is the Ordinal assigned below and survives the later
	// collapse and sort passes.
	disc, discValues, hasDisc := discriminatorValues(schema, branches)

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
		if branch.Value != nil && branch.Value.Type != nil && branch.Value.Type.Is(openapi3.TypeNull) {
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
		if hasDisc {
			b.DiscriminatorValue = discValues[branchIdx]
		}
		classified = append(classified, b)
		branchIdx++
	}

	if len(classified) < 2 {
		// Single non-null branch or no branches: not a union.
		if len(classified) == 1 {
			return classified[0].GoType
		}
		return goTypeRawMessage
	}

	// Branches that share a GoType are kept here. Reachability depends on which
	// decode strategy the union ends up with, and that is not assigned until the
	// IR phase (classifyUnions): a wire-decoded union can only ever reach the
	// first branch of a given type, while a request-selected union is chosen by
	// the caller, so As<Branch>() accessors over one Go type are all reachable
	// and distinct (AsAvg/AsSum/AsMin over SingleMetricAggregateBase). The Parse
	// phase cannot decide it; dropUnreachableBranches does, once the strategy is
	// known.

	// Collapse branches that are indistinguishable when decoded from the same
	// JSON token (e.g. int/int32/int64, or float32/float64): a try-each decoder
	// can only ever reach the first, so the narrower siblings are dead and risk
	// silent truncation. This can drop the union back to a single branch.
	classified = collapseEquivalentBranches(classified)
	if len(classified) < 2 {
		return classified[0].GoType
	}

	// A permissive string-enum branch (type X string, from a const-oneOf) and a
	// plain-string branch are both decoded from a JSON string and the enum
	// accepts any string, so the plain-string branch is a dead superset duplicate
	// (e.g. HighlighterType = builtin-enum | custom-string). Drop it, keeping the
	// enum, which collapses the union to the enum type -- a single named type the
	// exhaustive linter can check at switch sites.
	classified = w.collapseStringEnumWithString(classified)
	if len(classified) < 2 {
		return classified[0].GoType
	}

	// Disambiguate branches that share the same accessor Name.
	deduplicateAccessorNames(classified)

	// A discriminator names its branch outright, so decode order is irrelevant
	// to it. Order the branches newest-first only for the fallback decoders,
	// which attempt branches in slice order and want the most recent (and most
	// likely) schema first.
	if !hasDisc && branchesCollideOnTokenClass(classified) {
		sortBranchesNewestFirst(classified)
	}

	// A collapse pass above can drop a branch the discriminator resolved, which
	// would leave a wire value mapping to nothing. Re-verify that every surviving
	// branch still carries a value and that the values remain distinct; give the
	// discriminator up otherwise, since a decoder with an unreachable case is
	// worse than the fallback.
	if hasDisc && !discriminatorStillCovers(classified, disc) {
		disc, hasDisc = nil, false
		if branchesCollideOnTokenClass(classified) {
			sortBranchesNewestFirst(classified)
		}
	}

	name := schemaTypeName(schemaKey, false)
	shared := isSharedSchema(schemaKey)

	// A branch const is "<Union><Branch>Type", which lands in the same
	// package-level namespace as every union's "<Union>Type" enum type. Those can
	// collide across DIFFERENT unions, which deduplicateAccessorNames cannot see
	// because it only compares branches within one union.
	renameBranchesShadowingTypeNames(name, classified)

	ownerGroup := group
	if g := schemaGroup(schemaKey); g != "" {
		ownerGroup = g
	}

	t := &goType{
		Name:            name,
		Pkg:             typePkg(shared, ownerGroup, w.registry),
		SchemaRef:       schemaKey,
		IsShared:        shared,
		IsUnion:         true,
		IsAmbiguousWire: branchesCollideOnTokenClass(classified),
		Branches:        classified,
		Comment:         schema.Description,
	}
	if hasDisc {
		t.Discriminator = disc
		w.resolveDiscriminatorFields(classified, disc.PropertyName)
	}

	if registered, ok := w.registry.register(t); ok {
		return registered.Name
	}
	if existing, ok := w.registry.lookup(schemaKey); ok {
		return existing.Name
	}
	return name
}

// resolveDiscriminatorFields records, for each branch, the Go field that carries
// the discriminator property, so the generated constructor can set it.
//
// A union built in Go rather than decoded would otherwise marshal without its
// discriminator: NewCommonMappingPropertyFromKeywordProperty leaves the branch's
// `Type string` field at "", MarshalJSON emits `"type":""`, and feeding those
// bytes back to UnmarshalJSON fails with an unknown-discriminator error. The
// value is fixed per branch by the spec, so the constructor can supply it.
//
// Only a plain (non-pointer) string field is eligible. The field may sit on an
// allOf base rather than the branch struct itself, so the search follows embeds.
func (w *walker) resolveDiscriminatorFields(branches []unionBranch, propertyName string) {
	for i := range branches {
		b := &branches[i]
		if b.DiscriminatorValue == "" {
			continue
		}
		b.DiscriminatorField = w.findStringFieldByJSONName(unwrapTypeName(b.GoType), propertyName, make(set[string]))
	}
}

// findStringFieldByJSONName returns the Go name of the plain-string field whose
// JSON tag is jsonName on the named type, following embedded types. Returns ""
// when no such field exists, the field is a pointer, or the type is not a
// registered struct.
//
// visited guards against an embed cycle, which the registry does not itself rule
// out.
func (w *walker) findStringFieldByJSONName(typeName, jsonName string, visited set[string]) string {
	if typeName == "" || visited.has(typeName) {
		return ""
	}
	visited.add(typeName)

	t, ok := w.registry.lookupByName(typeName)
	if !ok {
		return ""
	}
	for _, f := range t.Fields {
		if f.JSONName == jsonName {
			// A pointer or non-string field is not something the constructor can
			// assign a bare wire value to.
			if f.GoType == "string" && !f.IsPointer {
				return f.GoName
			}
			return ""
		}
	}
	for _, f := range t.Fields {
		if !f.IsEmbed {
			continue
		}
		if name := w.findStringFieldByJSONName(unwrapTypeName(f.GoType), jsonName, visited); name != "" {
			return name
		}
	}
	return ""
}

// discriminatorStillCovers reports whether every branch left after the collapse
// passes carries a distinct discriminator value, and whether the spec's
// x-default still names one of them.
//
// The collapse passes run after discriminatorValues resolved, and each can drop
// a branch (collapseEquivalentBranches drops narrower numeric siblings,
// collapseStringEnumWithString drops a plain-string branch). Dropping a branch
// the discriminator mapped would leave that wire value decoding into nothing, so
// the union falls back rather than emitting a decoder with a dead case. In
// practice no discriminated union in the spec has numeric or string branches for
// those passes to touch, so this guard does not fire today; it keeps the
// invariant local instead of resting on that coincidence.
func discriminatorStillCovers(branches []unionBranch, disc *unionDiscriminator) bool {
	seen := make(set[string], len(branches))
	for _, b := range branches {
		if b.DiscriminatorValue == "" || seen.has(b.DiscriminatorValue) {
			return false
		}
		seen.add(b.DiscriminatorValue)
	}
	return disc.DefaultValue == "" || seen.has(disc.DefaultValue)
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

	if s.Type.Is(openapi3.TypeArray) {
		elemType := goTypeRawMessage
		if s.Items != nil {
			elemType = w.walkSchema(s.Items, parentKey+"Item", group, false)
		}
		sliceType := "[]" + elemType
		return unionBranch{
			Name:         "Array",
			GoType:       sliceType,
			TokenClass:   ir.TokenArray,
			VersionAdded: versionAdded,
		}
	}

	if s.Type.Is(openapi3.TypeObject) {
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
			TokenClass:   ir.TokenObject,
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
	if goTypeName != "" && goTypeName != goTypeRawMessage {
		return unionBranch{
			Name:         name,
			GoType:       goTypeName,
			TokenClass:   ir.TokenObject,
			Required:     flattenRequired(s),
			IsRef:        true,
			VersionAdded: versionAdded,
		}
	}

	// Properties present but unresolvable to a named type: raw map fallback.
	return unionBranch{
		Name:         "Map",
		GoType:       "map[string]json.RawMessage",
		TokenClass:   ir.TokenObject,
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
		if br.Value != nil && br.Value.Type != nil && br.Value.Type.Is(openapi3.TypeNull) {
			continue
		}
		if br.Ref == "" && br.Value != nil && br.Value.Type != nil && br.Value.Type.Is(openapi3.TypeObject) {
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
	if goTypeName == "" || goTypeName == goTypeRawMessage {
		return unionBranch{}
	}

	// Without a version, sortBranchesNewestFirst orders $ref branches as if
	// unversioned. The annotation may sit beside the $ref or on the schema it
	// resolves to; refExtensionString prefers the former.
	versionAdded := refExtensionString(ref, extVersionAdded)

	if isPrimitiveType(goTypeName) {
		return unionBranch{
			Name:         primitiveBranchName(goTypeName),
			GoType:       goTypeName,
			TokenClass:   tokenClassForPrimitive(goTypeName),
			VersionAdded: versionAdded,
		}
	}

	if strings.HasPrefix(goTypeName, "map[") {
		return unionBranch{Name: "Map", GoType: goTypeName, TokenClass: ir.TokenObject, VersionAdded: versionAdded}
	}
	if strings.HasPrefix(goTypeName, "[]") {
		return unionBranch{Name: "Array", GoType: goTypeName, TokenClass: ir.TokenArray, VersionAdded: versionAdded}
	}

	branchName := deriveBranchName(ref, goTypeName, key)
	required := flattenRequired(ref.Value)

	return unionBranch{
		Name:         branchName,
		GoType:       goTypeName,
		SchemaKey:    key,
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
	seen := make(set[string])
	var out []string
	var walk func(*openapi3.Schema)
	walk = func(sch *openapi3.Schema) {
		if sch == nil {
			return
		}
		for _, k := range sch.Required {
			if !seen.has(k) {
				seen.add(k)
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
func tokenClassForSchemaValue(schema *openapi3.Schema) ir.TokenClass {
	if schema == nil {
		return ir.TokenObject
	}
	if schema.Type == nil {
		if len(schema.Properties) > 0 || len(schema.AllOf) > 0 {
			return ir.TokenObject
		}
		return ir.TokenObject
	}
	switch {
	case schema.Type.Is(openapi3.TypeObject):
		return ir.TokenObject
	case schema.Type.Is(openapi3.TypeArray):
		return ir.TokenArray
	case schema.Type.Is(openapi3.TypeString):
		return ir.TokenString
	case schema.Type.Is(openapi3.TypeInteger), schema.Type.Is(openapi3.TypeNumber):
		return ir.TokenNumber
	case schema.Type.Is(openapi3.TypeBoolean):
		return ir.TokenBool
	}
	return ir.TokenObject
}

// tokenClassForPrimitive maps a Go type name to its JSON token class.
func tokenClassForPrimitive(goType string) ir.TokenClass {
	switch goType {
	case "string":
		return ir.TokenString
	case "bool":
		return ir.TokenBool
	case "int", "int32", "int64", "float32", "float64":
		return ir.TokenNumber
	}
	if strings.HasPrefix(goType, "[]") {
		return ir.TokenArray
	}
	if strings.HasPrefix(goType, "map[") {
		return ir.TokenObject
	}
	return ir.TokenObject
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

// renameBranchesShadowingTypeNames disambiguates a branch whose discriminant
// const would collide with some union's generated enum TYPE name.
//
// Both live in one package-level namespace: a union emits `type <Union>Type int`
// and one `<Union><Branch>Type` const per branch. So a union X with a branch B
// collides with a sibling union named XB -- the const XBType is also XB's enum
// type name, and the package no longer compiles.
//
// The spec hits this three times, all the same shape: a `<Thing>` union
// (string | <Thing>Definition) whose Definition branch sits beside the
// `<Thing>Definition` union it points at. CharFilter/CharFilterDefinition,
// TokenFilter/TokenFilterDefinition, and Tokenizer/TokenizerDefinition.
//
// The branch takes the referenced schema's own local name instead, which is
// unambiguous and more descriptive than the colliding short form: Definition ->
// CharFilterDefinition, giving CommonAnalysisCharFilterCharFilterDefinitionType.
//
// The replacement comes from the schema KEY's local segment, never from the
// branch's Go type name. The Go type name carries the group prefix the union name
// already supplies, so using it would reintroduce the very stutter this naming
// scheme exists to avoid
// (CommonAnalysisCharFilterCommonAnalysisCharFilterDefinitionType, 62 chars,
// against 48 for the local form). A branch with no schema key is skipped
// outright, since no unqualified name is recoverable for it.
//
// Only $ref branches are considered; see the loop for why inline branches are
// exempt. The collision is decidable from the two names alone, so this needs no
// registry lookup and does not depend on which union is walked first.
// deduplicateAccessorNames runs before this and handles the within-union case;
// this handles only the cross-union one.
func renameBranchesShadowingTypeNames(unionName string, branches []unionBranch) {
	for i := range branches {
		b := &branches[i]
		// Only $ref branches can collide. An INLINE object branch's type name is
		// derived as "<Union><Branch>" by construction (see
		// classifyObjectBranch), so it matches the test below every time -- but
		// that type is the union's own child, not an independent sibling union, so
		// there is nothing to disambiguate against. Renaming those would corrupt
		// every inline branch name.
		if !b.IsRef || b.SchemaKey == "" {
			continue
		}
		branchType := unwrapTypeName(b.GoType)
		// The const emitted for this branch is "<unionName><b.Name>Type"; the
		// branch's own type emits "<branchType>Type". They collide exactly when
		// the two stems match.
		if unionName+b.Name != branchType {
			continue
		}
		if replacement := schemaLocalGoName(b.SchemaKey); replacement != "" && replacement != b.Name {
			b.Name = replacement
		}
	}
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
// deriveBranchName names a union branch. The name becomes the branch's accessor,
// its discriminant const suffix, and its String() value, so all three stay the
// same token by construction.
//
// schemaKey is the branch's own $ref key, which may differ from goTypeName when
// the referenced schema collapsed onto another type (see
// walker.resolveCollapsedBase).
//
// The name is the schema's LOCAL segment (the part after "___"), not its fully
// qualified Go type name. A union and its branches almost always live in the same
// spec group, so the qualified name repeats the group prefix the union name
// already carries: _common.analysis___CustomAnalyzer as a branch of
// _common.analysis___Analyzer would otherwise yield the const
// CommonAnalysisAnalyzerCommonAnalysisCustomAnalyzerType. Using the local segment
// gives CommonAnalysisAnalyzerCustomAnalyzerType.
//
// Branches whose local names collide within one union are disambiguated by
// deduplicateAccessorNames, which is why this can safely discard the qualifier.
func deriveBranchName(ref *openapi3.SchemaRef, goTypeName, schemaKey string) string {
	// Prefer the spec title if available.
	if ref.Value != nil && ref.Value.Title != "" {
		return baseGoName(ref.Value.Title)
	}
	// Name the branch after the schema the union actually references, not after
	// whatever that schema resolved to. A collapsed schema resolves to its base,
	// whose name may carry the spec's "Base" suffix -- so mget's GetResult branch
	// would otherwise generate AsGetResultBase()/GetResultBase(), advertising a
	// name the union never mentions.
	if name := schemaLocalGoName(schemaKey); name != "" {
		return name
	}
	// Normalize the Go type name through baseGoName to strip dotted
	// package qualifiers and other non-identifier punctuation.
	return baseGoName(goTypeName)
}

// schemaLocalGoName renders the Go-identifier form of a schema key's local
// segment: the part after "___", which is the schema's own name within its group
// ("_common.analysis___CustomAnalyzer" -> "CustomAnalyzer"). Keys with no "___"
// separator (inline child keys such as "<parent>.<field>") have no local segment
// to isolate and yield "".
//
// This deliberately skips schemaTypeName's group-prefixing: the caller wants the
// unqualified name precisely because the enclosing union name already supplies
// the group context.
func schemaLocalGoName(schemaKey string) string {
	_, local, ok := strings.Cut(schemaKey, "___")
	if !ok || local == "" {
		return ""
	}
	return pascalFromSegments(local)
}

// isPrimitiveType returns true if the Go type name is a builtin primitive.
func isPrimitiveType(goType string) bool {
	switch goType {
	case "string", "bool", "int", "int32", "int64", "float32", "float64":
		return true
	}
	return false
}

// decodeEquivalentGroups lists Go primitive types that decode from the same
// JSON token and are therefore indistinguishable in a try-each union: only the
// first such branch attempted is ever reachable. Each group is ordered widest-
// first; when a union declares more than one member of a group, only the
// widest (first-listed) survives so the kept accessor never truncates a value
// the dropped branches could have held. Integer and float groups stay separate
// because a float branch accepts integers but an int branch rejects decimals,
// so the two classes remain mutually reachable. string and bool need no group:
// each has a single Go type; exact GoType duplicates are left for
// dropUnreachableBranches, which runs once the decode state is known and can
// tell a dead duplicate from a distinct lazy accessor.
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
	drop := make(set[int])
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
				drop.add(i)
			}
		}
	}
	if len(drop) == 0 {
		return branches
	}
	result := make([]unionBranch, 0, len(branches)-len(drop))
	for i := range branches {
		if !drop.has(i) {
			result = append(result, branches[i])
		}
	}
	return result
}

// collapseStringEnumWithString drops plain-string branches from a union that
// also has a permissive string-enum branch (a type X string generated from a
// const-oneOf). Such an enum decodes from a JSON string and accepts ANY string,
// so a sibling plain-string branch is a dead superset duplicate that a
// value-agnostic decoder could never distinguish (e.g. HighlighterType =
// builtin-enum | custom-string). Keeping only the enum collapses the union to
// that single named type, which the exhaustive linter can check at switch sites.
//
// Invariant for the caller's classified[0] access: this only ever DROPS
// plain-string branches, and only when at least one string-enum branch exists to
// keep. So a non-empty input yields a non-empty output (the enum branch always
// survives); it never empties the slice. When there is no string-enum branch, or
// no plain-string branch to drop, the input is returned unchanged.
func (w *walker) collapseStringEnumWithString(branches []unionBranch) []unionBranch {
	hasStringEnum := false
	for _, b := range branches {
		if t, ok := w.registry.lookupByName(b.GoType); ok && t.IsStringEnum {
			hasStringEnum = true
			break
		}
	}
	if !hasStringEnum {
		return branches
	}

	result := make([]unionBranch, 0, len(branches))
	for _, b := range branches {
		if b.GoType == "string" {
			continue // dead: the string-enum branch already accepts any string
		}
		result = append(result, b)
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
