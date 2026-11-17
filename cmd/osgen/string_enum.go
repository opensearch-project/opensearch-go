// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/token"
	"io"
	"maps"
	"slices"

	"github.com/getkin/kin-openapi/openapi3"
)

// stringEnumDenyList lists schema keys that enumParser must not type, keeping
// them a plain string instead. Detection is default-on: any schema matching an
// accepted enum shape is typed unless it appears here.
//
// Empty today -- every accepted enum schema in the bundled spec produces a clean,
// useful typed enum. Add an entry (with a one-line rationale) if a future schema,
// of either shape, should deliberately stay untyped.
//
//nolint:gochecknoglobals // const-ish read-only deny-list
var stringEnumDenyList = set[string]{}

// constEnumValue pairs a wire value with its per-branch description and
// version/deprecation metadata (used to annotate the generated const's doc).
type constEnumValue struct {
	Value             string
	Description       string
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
}

// enumShape is the schema shape an enum was recognized from. The shapes are
// ordered from narrowest to widest; a parse given a widest shape accepts every
// narrower one too.
type enumShape int

const (
	// enumShapeNone: the schema is not an accepted enum.
	enumShapeNone enumShape = iota
	// enumShapeConst: every oneOf/anyOf branch is {type: string, const: X}
	// (e.g. _common___NodeRole). The only shape response and body fields accept.
	enumShapeConst
	// enumShapeParam: the remaining enum spellings in the spec, accepted only for
	// query parameters: a top-level {type: string, enum: [...]}, oneOf branches
	// carrying enum lists instead of a const, and a {type: boolean} branch beside
	// string consts (skipped, like a null branch).
	enumShapeParam
	// enumShapeIntOrConst: an integer or one of a few keywords
	// (wait_for_active_shards: an integer or "all"; slices: an integer or
	// "auto"). Accepted only for query parameters.
	enumShapeIntOrConst
)

// enumParser recognizes string-enum schemas and returns their const values (with
// per-value descriptions) ready to emit as a Go string type.
//
// Detection is default-on: the shape itself opts a schema in, unless its key is
// in deny (production passes the package-global stringEnumDenyList; tests pass
// their own map so they never mutate global state).
//
// Branches are version-filtered against vr, mirroring how struct fields are
// filtered in walkProperties: a branch carrying x-version-added / -removed /
// -deprecated outside the range is dropped. Surviving values are then reduced to
// those that yield a VALID, UNIQUE Go const identifier (typeName+enumValueIdent):
//
//   - A value whose identifier segment is empty or produces a non-identifier
//     const name is skipped with a note. This keeps the generator panic-proof as
//     the spec evolves: convertEnumMembers panics on a bad identifier (correct
//     for the closed int-enum path), so the string-enum path must never hand it
//     one.
//   - Values are deduplicated by generated const name, keeping the first branch.
//     This collapses both true duplicates (NodeRole lists `search` twice across
//     version boundaries) and distinct-but-colliding casings (TranslogDurability
//     lists ASYNC and async, which PascalCase to the same identifier); the first-
//     listed spelling wins its const, and the dropped spelling remains usable as
//     a raw string.
//
// Whenever a DISTINCT value is dropped (bad identifier, or a casing that lost the
// dedup), a note is written to warnW so the lost spelling is visible at
// generation time rather than silent (mirrors typeRegistry.checkCollisions).
type enumParser struct {
	vr    VersionRange
	deny  set[string]
	warnW io.Writer
}

// constOneOf parses the strict enumShapeConst shape and reports ok=false for any
// other, so callers fall back to a plain string.
func (p enumParser) constOneOf(typeName, schemaKey string, schema *openapi3.Schema) ([]constEnumValue, bool) {
	values, shape := p.parse(typeName, schemaKey, schema, enumShapeConst)
	return values, shape != enumShapeNone
}

// paramEnum parses any shape a query parameter accepts and reports which one
// matched, enumShapeNone when none did.
func (p enumParser) paramEnum(typeName, schemaKey string, schema *openapi3.Schema) ([]constEnumValue, enumShape) {
	if values, shape := p.parse(typeName, schemaKey, schema, enumShapeParam); shape != enumShapeNone {
		return values, shape
	}
	return p.intOrConst(typeName, schemaKey, schema)
}

// intOrConst parses the enumShapeIntOrConst shape and returns its keyword consts.
// The schema is a oneOf with exactly one integer branch and at least one const
// branch. A branch may nest one oneOf level: an integer branch whose oneOf holds
// an integer beside a numeric string pattern, a const branch whose oneOf holds
// consts and null. Keywords go through parse, so version filtering, identifier
// checks and dedup match the other shapes.
func (p enumParser) intOrConst(typeName, schemaKey string, schema *openapi3.Schema) ([]constEnumValue, enumShape) {
	if schema == nil {
		return nil, enumShapeNone
	}
	var keywords openapi3.SchemaRefs
	counts := 0
	for _, ref := range schema.OneOf {
		if ref == nil || ref.Value == nil {
			return nil, enumShapeNone
		}
		s := ref.Value
		switch {
		case s.Type != nil && s.Type.Is(openapi3.TypeNull):
		case s.Type != nil && s.Type.Is(openapi3.TypeInteger):
			counts++
		case len(s.OneOf) > 0 && oneOfBranchOfType(s, openapi3.TypeInteger) != nil:
			counts++
		case len(s.OneOf) > 0:
			keywords = append(keywords, s.OneOf...)
		default:
			keywords = append(keywords, ref)
		}
	}
	if counts != 1 || len(keywords) == 0 {
		return nil, enumShapeNone
	}
	values, shape := p.parse(typeName, schemaKey, &openapi3.Schema{OneOf: keywords}, enumShapeConst)
	if shape == enumShapeNone {
		return nil, enumShapeNone
	}
	return values, enumShapeIntOrConst
}

// oneOfBranchOfType returns the first direct oneOf branch of s whose type is
// typ, or nil.
func oneOfBranchOfType(s *openapi3.Schema, typ string) *openapi3.Schema {
	for _, b := range s.OneOf {
		if b != nil && b.Value != nil && b.Value.Type != nil && b.Value.Type.Is(typ) {
			return b.Value
		}
	}
	return nil
}

// parse recognizes enumShapeConst, and enumShapeParam too when widest is
// enumShapeParam, and reports the narrowest shape the schema fits.
func (p enumParser) parse(typeName, schemaKey string, schema *openapi3.Schema, widest enumShape) ([]constEnumValue, enumShape) {
	if schema == nil || p.deny.has(schemaKey) {
		return nil, enumShapeNone
	}
	allowParam := widest >= enumShapeParam
	shape := enumShapeConst
	branches := schema.OneOf
	if len(branches) == 0 {
		branches = schema.AnyOf
	}
	var valueDocs map[string]string
	if len(branches) == 0 {
		if !allowParam || schema.Type == nil || !schema.Type.Is(openapi3.TypeString) {
			return nil, enumShapeNone
		}
		// The schema is its own single branch. Its description documents the
		// type, so the copy standing in for the branch drops it rather than
		// repeating it on every value; x-enum-descriptions documents the values.
		self := *schema
		self.Description = ""
		branches = openapi3.SchemaRefs{{Value: &self}}
		valueDocs = extensionStringMap(schema.Extensions, extEnumDescriptions)
		shape = enumShapeParam
	}

	var raw []constEnumValue
	for _, ref := range branches {
		if ref == nil || ref.Value == nil {
			return nil, enumShapeNone
		}
		s := ref.Value
		// Tolerate an explicit null branch (nullable union spelling); it carries
		// no const and is simply skipped.
		if s.Type != nil && s.Type.Is(openapi3.TypeNull) {
			continue
		}
		if allowParam && s.Type != nil && s.Type.Is(openapi3.TypeBoolean) {
			shape = enumShapeParam
			continue
		}
		if s.Type == nil || !s.Type.Is(openapi3.TypeString) {
			return nil, enumShapeNone
		}
		var values []string
		if cv, ok := s.Const.(string); ok {
			values = []string{cv}
		} else if allowParam && len(s.Enum) > 0 {
			values = enumStringValues(s.Enum)
			shape = enumShapeParam
		}
		if len(values) == 0 {
			return nil, enumShapeNone
		}

		vAdded := extensionString(s.Extensions, extVersionAdded)
		vRemoved := extensionString(s.Extensions, extVersionRemoved)
		vDeprecated := extensionString(s.Extensions, extVersionDeprecated)
		if !p.vr.Includes(vAdded, vRemoved, vDeprecated) {
			continue
		}

		for _, cv := range values {
			desc := s.Description
			if d, ok := valueDocs[cv]; ok {
				desc = d
			}
			raw = append(raw, constEnumValue{
				Value:             cv,
				Description:       desc,
				VersionAdded:      vAdded,
				VersionDeprecated: vDeprecated,
				DeprecationMsg:    extensionString(s.Extensions, extDeprecationMessage),
			})
		}
	}
	p.warnUnknownValueDocs(typeName, valueDocs, raw)

	out := p.dedup(typeName, raw)
	if len(out) == 0 {
		return nil, enumShapeNone
	}
	return out, shape
}

// warnUnknownValueDocs notes each x-enum-descriptions key that names no value
// of the enum, so a description left behind by a renamed value is visible at
// generation time.
func (p enumParser) warnUnknownValueDocs(typeName string, valueDocs map[string]string, values []constEnumValue) {
	if p.warnW == nil || len(valueDocs) == 0 {
		return
	}
	known := make(set[string], len(values))
	for _, cv := range values {
		known.add(cv.Value)
	}
	for _, v := range slices.Sorted(maps.Keys(valueDocs)) {
		if !known.has(v) {
			fmt.Fprintf(p.warnW, "osgen: %s: %s names %q, which is not a value of the enum\n", typeName, extEnumDescriptions, v)
		}
	}
}

// dedup reduces raw to values with a valid, unique const identifier, keeping
// the first branch to win each identifier. seen maps const name -> the value
// that took it, so a later distinct value colliding on the same name is
// reported.
func (p enumParser) dedup(typeName string, raw []constEnumValue) []constEnumValue {
	seen := make(map[string]string, len(raw))
	out := make([]constEnumValue, 0, len(raw))
	for _, cv := range raw {
		ident := enumValueIdent(cv.Value)
		constName := typeName + ident
		if ident == "" || !token.IsIdentifier(constName) {
			if p.warnW != nil {
				fmt.Fprintf(p.warnW, "osgen: %s: value %q skipped; yields no valid Go const identifier (still usable as a raw string)\n",
					typeName, cv.Value)
			}
			continue
		}
		if kept, dup := seen[constName]; dup {
			if kept != cv.Value && p.warnW != nil {
				fmt.Fprintf(p.warnW, "osgen: %s: value %q dropped; const %s already held by %q (still usable as a raw string, no dedicated const)\n",
					typeName, cv.Value, constName, kept)
			}
			continue
		}
		seen[constName] = cv.Value
		out = append(out, cv)
	}
	return out
}
