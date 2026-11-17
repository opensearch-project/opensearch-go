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

	"github.com/getkin/kin-openapi/openapi3"
)

// stringEnumDenyList lists schema keys that parseConstOneOf and parseParamEnum
// must not type, keeping them a plain string instead. Detection is default-on:
// any schema matching an accepted enum shape is typed unless it appears here.
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

// parseConstOneOf recognizes a schema whose oneOf/anyOf branches are all
// {type: string, const: <value>} entries and returns the const values (with
// per-branch descriptions) ready to emit as a named Go string type. It reports
// ok=false for any other shape, or when denied/filtered down to nothing, so
// callers fall back to a plain string.
//
// Detection is default-on: the shape itself opts a schema in, unless schemaKey
// is in deny (the production caller passes the package-global
// stringEnumDenyList; tests pass their own map so they never mutate global
// state).
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
func parseConstOneOf(
	typeName, schemaKey string,
	schema *openapi3.Schema,
	vr VersionRange,
	deny set[string],
	warnW io.Writer,
) ([]constEnumValue, bool) {
	return parseEnumValues(typeName, schemaKey, schema, vr, deny, warnW, false)
}

// parseParamEnum is parseConstOneOf for query-parameter schemas, which also
// accept the remaining enum spellings in the spec: a top-level
// {type: string, enum: [...]}, oneOf branches carrying enum lists instead of a
// const, and a {type: boolean} branch beside string consts (skipped, like a null
// branch). Response and body fields keep using the strict parseConstOneOf, so
// their Go types do not change.
func parseParamEnum(
	typeName, schemaKey string,
	schema *openapi3.Schema,
	vr VersionRange,
	deny set[string],
	warnW io.Writer,
) ([]constEnumValue, bool) {
	return parseEnumValues(typeName, schemaKey, schema, vr, deny, warnW, true)
}

// parseIntOrConstEnum recognizes the query-parameter shape "an integer or one of
// a few keywords" (wait_for_active_shards: an integer or "all"; slices: an
// integer or "auto") and returns the keyword consts. The schema is a oneOf with
// exactly one integer branch and at least one const branch. A branch may nest
// one oneOf level: an integer branch whose oneOf holds an integer beside a
// numeric string pattern, a const branch whose oneOf holds consts and null.
// Keywords go through parseEnumValues, so version filtering, identifier checks
// and dedup match the other enum shapes. It reports ok=false for any other shape.
func parseIntOrConstEnum(
	typeName, schemaKey string,
	schema *openapi3.Schema,
	vr VersionRange,
	deny set[string],
	warnW io.Writer,
) ([]constEnumValue, bool) {
	if schema == nil {
		return nil, false
	}
	var keywords openapi3.SchemaRefs
	counts := 0
	for _, ref := range schema.OneOf {
		if ref == nil || ref.Value == nil {
			return nil, false
		}
		s := ref.Value
		switch {
		case s.Type != nil && s.Type.Is(openapi3.TypeNull):
		case s.Type != nil && s.Type.Is(openapi3.TypeInteger):
			counts++
		case len(s.OneOf) > 0 && hasIntegerBranch(s):
			counts++
		case len(s.OneOf) > 0:
			keywords = append(keywords, s.OneOf...)
		default:
			keywords = append(keywords, ref)
		}
	}
	if counts != 1 || len(keywords) == 0 {
		return nil, false
	}
	return parseEnumValues(typeName, schemaKey, &openapi3.Schema{OneOf: keywords}, vr, deny, warnW, false)
}

// hasIntegerBranch reports whether any direct oneOf branch of s is an integer.
func hasIntegerBranch(s *openapi3.Schema) bool {
	for _, b := range s.OneOf {
		if b != nil && b.Value != nil && b.Value.Type != nil && b.Value.Type.Is(openapi3.TypeInteger) {
			return true
		}
	}
	return false
}

// parseEnumValues implements parseConstOneOf and parseParamEnum; lenient selects
// the extra query-parameter shapes described on parseParamEnum.
func parseEnumValues(
	typeName, schemaKey string,
	schema *openapi3.Schema,
	vr VersionRange,
	deny set[string],
	warnW io.Writer,
	lenient bool,
) ([]constEnumValue, bool) {
	if schema == nil {
		return nil, false
	}
	if deny.has(schemaKey) {
		return nil, false
	}
	branches := schema.OneOf
	if len(branches) == 0 {
		branches = schema.AnyOf
	}
	if len(branches) == 0 {
		if lenient && schema.Type != nil && schema.Type.Is(openapi3.TypeString) {
			// The schema is its own single branch. Its description documents the
			// type, so the copy standing in for the branch drops it rather than
			// repeating it on every value.
			self := *schema
			self.Description = ""
			branches = openapi3.SchemaRefs{{Value: &self}}
		} else {
			return nil, false
		}
	}

	var raw []constEnumValue
	sawConst := false
	for _, ref := range branches {
		if ref == nil || ref.Value == nil {
			return nil, false
		}
		s := ref.Value
		// Tolerate an explicit null branch (nullable union spelling); it carries
		// no const and is simply skipped.
		if s.Type != nil && s.Type.Is(openapi3.TypeNull) {
			continue
		}
		if lenient && s.Type != nil && s.Type.Is(openapi3.TypeBoolean) {
			continue
		}
		if s.Type == nil || !s.Type.Is(openapi3.TypeString) {
			return nil, false
		}
		var values []string
		if cv, ok := s.Const.(string); ok {
			values = []string{cv}
		} else if lenient && len(s.Enum) > 0 {
			values = enumStringValues(s.Enum)
		}
		if len(values) == 0 {
			return nil, false
		}

		vAdded := extensionString(s.Extensions, extVersionAdded)
		vRemoved := extensionString(s.Extensions, extVersionRemoved)
		vDeprecated := extensionString(s.Extensions, extVersionDeprecated)
		if !vr.Includes(vAdded, vRemoved, vDeprecated) {
			continue
		}

		sawConst = true
		for _, cv := range values {
			raw = append(raw, constEnumValue{
				Value:             cv,
				Description:       s.Description,
				VersionAdded:      vAdded,
				VersionDeprecated: vDeprecated,
				DeprecationMsg:    extensionString(s.Extensions, extDeprecationMessage),
			})
		}
	}
	if !sawConst {
		return nil, false
	}

	// Reduce to values with a valid, unique const identifier, keeping the first
	// branch to win each identifier. seen maps const name -> the value that took
	// it, so a later distinct value colliding on the same name is reported.
	seen := make(map[string]string, len(raw))
	out := make([]constEnumValue, 0, len(raw))
	for _, cv := range raw {
		ident := enumValueIdent(cv.Value)
		constName := typeName + ident
		if ident == "" || !token.IsIdentifier(constName) {
			if warnW != nil {
				fmt.Fprintf(warnW, "osgen: %s: value %q skipped; yields no valid Go const identifier (still usable as a raw string)\n",
					typeName, cv.Value)
			}
			continue
		}
		if kept, dup := seen[constName]; dup {
			if kept != cv.Value && warnW != nil {
				fmt.Fprintf(warnW, "osgen: %s: value %q dropped; const %s already held by %q (still usable as a raw string, no dedicated const)\n",
					typeName, cv.Value, constName, kept)
			}
			continue
		}
		seen[constName] = cv.Value
		out = append(out, cv)
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
