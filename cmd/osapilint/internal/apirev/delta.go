// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// delta.go derives the field-level migration delta between a v4 struct and its
// v5 counterpart, per fully-qualified type. This is the type-scoped ground truth
// the rewriter applies - it never guesses from a bare field name across a
// package, which matters because the same field name fans in across types
// (EnableMetrics on both opensearch.Config and opensearchtransport.Config).

// majorVersionSeg matches the "/vN" module-major-version segment in an import
// path (e.g. ".../opensearch-go/v4/opensearchapi" or a trailing ".../opensearch-go/v4").
// The major version is part of the path, so a v4 package and its v5 successor
// never share an exact path; pairing must compare paths with this segment
// normalized away.
var majorVersionSeg = regexp.MustCompile(`/v[0-9]+(/|$)`)

// versionAgnostic strips the module-major-version segment so v4 and v5 package
// paths for the same logical package compare equal. A "/vN" in the middle of the
// path collapses to a single "/"; a trailing "/vN" is removed entirely (no
// dangling slash), so ".../opensearch-go/v4" and ".../opensearch-go/v5" both
// become ".../opensearch-go".
func versionAgnostic(pkgPath string) string {
	return majorVersionSeg.ReplaceAllStringFunc(pkgPath, func(m string) string {
		if len(m) > 0 && m[len(m)-1] == '/' {
			return "/" // "/vN/" -> "/"
		}
		return "" // trailing "/vN" -> ""
	})
}

// FieldChange is one field-level change within a single struct.
//
// Kinds:
//   - "rename":      field key/name changed 1:1; a Note warns if its type also changed.
//   - "pointerWrap": field's value type became a pointer to the same type (wrap
//     a literal in &, any other value in new(x)).
//   - "remove":      field ceased to exist as a settable knob; dropping a
//     composite-literal key for it is correct (e.g. EnableMetrics). NEVER
//     applied to a field access - only literal keys. Only emitted when an
//     explicit FieldDisposition classifies the removal.
//   - "manual":      field vanished but its data/behavior relocated (e.g. a v5
//     Resp collapsed to a raw Body json.RawMessage, so resp.Deleted must become
//     a decode call), or its type changed in a way no mechanical rewrite covers
//     (e.g. it became a pointer to a different type, int -> *int64, or a value
//     of the old type no longer fits, *int -> int). Reported
//     for a human wherever it is set or read: even a read that still compiles
//     can change behavior (printing a *string prints the pointer).
//   - "unclassified": field vanished from the target and NO FieldDisposition
//     covers it. The tool cannot know whether it was renamed (rewrite) or
//     removed (drop), so it refuses to guess. If the consumer actually
//     references such a field, that is an osapilint bug - the field table is
//     incomplete - and the rewrite fails loudly rather than silently dropping a
//     value. See the linter's unclassified handling.
type FieldChange struct {
	Kind    string `json:"kind"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	NewType string `json:"newType,omitempty"`
	Note    string `json:"note,omitempty"` // manual guidance, including type-changing renames
}

// FieldChange kinds. See the FieldChange doc comment for the meaning of each.
const (
	KindRename       = "rename"
	KindPointerWrap  = "pointerWrap"
	KindRemove       = "remove"
	KindManual       = "manual"
	KindUnclassified = "unclassified"
)

// StructDelta is the set of field changes for one struct, identified by its
// fully-qualified source and target type keys ("<pkgPath>.<Name>").
type StructDelta struct {
	From    string        `json:"from"` // e.g. ".../v4/opensearchapi.DocumentGetReq"
	To      string        `json:"to"`   // e.g. ".../v5/opensearchapi.GetReq"
	Changes []FieldChange `json:"changes"`
}

// Delta is the whole source -> target migration delta, keyed by fully-qualified
// source type.
type Delta struct {
	Structs map[string]StructDelta `json:"structs"`
	// RemovedTypes is the set of fully-qualified source types ("<pkgPath>.<Name>")
	// that have NO counterpart in the target and are not covered by a TypeRename -
	// i.e. types deleted outright across the hop (e.g. the v2 opensearchapi.*Request
	// family removed in v3's client redesign). It is a set: the qualified name is the
	// map key and the value is always true. Unlike a vanished FIELD (which fails loud
	// only if referenced via an incomplete disposition table), a removed TYPE is a
	// known, deliberate deletion; the linter reports any reference to one as a manual
	// worklist item rather than guessing a shape-changing rewrite it cannot express.
	RemovedTypes map[string]bool `json:"removedTypes,omitempty"`
}

// TypeRename identifies a source type that was renamed in the target version, by
// unqualified name within a package pair. The linter resolves the source struct
// in FromPkgPath and its counterpart in ToPkgPath.
type TypeRename struct {
	FromPkgPath string
	FromName    string
	ToPkgPath   string
	ToName      string
}

// Field disposition actions: what to do with a source field that no longer
// exists on the paired target struct.
const (
	// ActionRename: the field was renamed; rewrite the key/selector to ToField.
	ActionRename = "rename"
	// ActionRemove: the field was genuinely removed; dropping a composite-literal
	// key for it is correct. An explicit entry documents that the removal is
	// intended (not an un-triaged gap).
	ActionRemove = "remove"
	// ActionManual: the field's data/behavior relocated; not mechanically
	// rewritable, so flag it for a human.
	ActionManual = "manual"
)

// FieldDisposition classifies a source struct field that vanished on the target,
// following the "match (source pkg + type + field) -> action" model. Field
// changes are a closed, discrete set - NOT something to infer heuristically:
// guessing from name/type similarity silently drops a caller's value when a
// struct changed by more than one field (e.g. SearchResp.Timeout -> TimedOut).
//
// Action is one of ActionRename / ActionRemove / ActionManual. For ActionRename,
// the To fields state the target type + field explicitly (rather than deriving
// them from the struct pairing) so the drift guard can confirm the target field
// exists on exactly the type it lands on, and so a field can move across a type
// rename in one entry. For remove/manual the To fields are unused.
//
// A vanished field with NO disposition is emitted as an "unclassified"
// FieldChange; the linter fails loudly if the consumer references it, so the
// table can never silently under-cover.
type FieldDisposition struct {
	FromPkgPath string
	FromType    string
	FromField   string
	Action      string
	ToPkgPath   string
	ToType      string
	ToField     string
	Note        string // optional human guidance, surfaced for ActionManual
}

// fieldDispKey is the lookup key for a field disposition: the qualified source
// type plus the source field name.
func fieldDispKey(pkgPath, typeName, field string) string {
	return pkgPath + "." + typeName + "#" + field
}

// DeriveDelta diffs every source struct against its target counterpart and
// returns the per-qualified-type field changes.
//
// Counterpart resolution:
//   - if a TypeRename covers the source struct, its target name/package are used;
//   - otherwise the source struct is paired with the identically named struct in
//     the SAME package path within the target (same-name survivor).
//
// A source struct with no target counterpart is recorded in RemovedTypes
// (genuinely removed; not mechanically migratable) so a reference to it is
// reported as a manual worklist item. Field pairing within a struct: pointerWrap
// when a surviving field became a pointer to the same type, manual when it
// became a pointer to another type or its value type no longer fits (see
// retypedValue), a rename to BodyReader for a raw Body split
// (see isBodyReaderSplit); for a vanished field, the
// FieldDisposition table's action (rename/remove/manual) if present, else
// "unclassified".
func DeriveDelta(from, to *Snapshot, renames []TypeRename, dispositions []FieldDisposition) Delta {
	// Index explicit renames by qualified source key.
	renameByFrom := map[string]TypeRename{}
	types := hopTypes{renamed: map[string]string{}, from: namedStructs(from), to: namedStructs(to)}
	for _, r := range renames {
		renameByFrom[r.FromPkgPath+"."+r.FromName] = r
		types.renamed[versionAgnosticType(r.FromPkgPath+"."+r.FromName)] = versionAgnosticType(r.ToPkgPath + "." + r.ToName)
	}
	// Index field dispositions by qualified-source-type + field.
	dispByFrom := map[string]FieldDisposition{}
	for _, d := range dispositions {
		dispByFrom[fieldDispKey(d.FromPkgPath, d.FromType, d.FromField)] = d
	}

	out := Delta{Structs: map[string]StructDelta{}, RemovedTypes: map[string]bool{}}
	for _, sFrom := range from.Structs {
		var sTo Struct
		var ok bool
		if r, has := renameByFrom[sFrom.Qualified()]; has {
			// explicit rename: resolve by the mapped target package + name
			sTo, ok = to.Lookup(r.ToPkgPath, r.ToName)
		} else {
			// same-name survivor: pair by version-agnostic package path + name,
			// because the module major version is baked into the import path
			// (v4/... vs v5/...) and would otherwise never match.
			sTo, ok = to.lookupVersionAgnostic(sFrom.PkgPath, sFrom.Name)
		}
		if !ok {
			// Removed in target: not mechanically migratable. Record it so the
			// linter can report a reference to it as a manual worklist item
			// (see the RemovedTypes doc), rather than silently dropping the type
			// and leaving the consumer with a bare "undefined" compile error.
			out.RemovedTypes[sFrom.Qualified()] = true
			continue
		}

		sd := StructDelta{From: sFrom.Qualified(), To: sTo.Qualified()}
		sd.Changes = diffFields(sFrom, sTo, dispByFrom, types)
		if len(sd.Changes) > 0 {
			out.Structs[sFrom.Qualified()] = sd
		}
	}
	return out
}

// diffFields computes the field-level changes from a source struct to its
// resolved target counterpart.
//
// Renames are NOT inferred. A field present only on the source side is resolved
// through the FieldDisposition table (dispByFrom, keyed by qualified source type
// + field): rename/remove/manual per the explicit action. A vanished field with
// NO disposition becomes "unclassified" - the tool refuses to guess, and the
// linter fails loudly if the consumer references it. This is deliberate:
// inferring from name/type similarity silently drops a caller's value whenever a
// struct changed by more than one field, which is common across a major version.
func diffFields(sFrom, sTo Struct, dispByFrom map[string]FieldDisposition, types hopTypes) []FieldChange {
	toByName := map[string]Field{}
	for _, f := range sTo.Fields {
		toByName[f.Name] = f
	}

	// If the target counterpart collapsed to a single raw json.RawMessage Body,
	// every vanished field's data now lives behind that Body and requires a decode.
	// This is a SAFE classifier (structural, not a rename guess): it never drops or
	// mis-maps a value, it only flags the access as manual. It applies when no
	// explicit disposition already rules on the field.
	collapsedToRawBody := isRawBodyCollapse(sTo)

	var changes []FieldChange
	for _, fFrom := range sFrom.Fields {
		fTo, still := toByName[fFrom.Name]
		switch {
		case still && isBodyReaderSplit(fFrom, fTo, toByName):
			// The raw request body moved to BodyReader when the target added a
			// typed Body. Same io.Reader value, new field name.
			changes = append(changes, FieldChange{Kind: KindRename, From: fFrom.Name, To: bodyReaderField, NewType: ioReader})
		case still && fTo.IsPointer() && !fFrom.IsPointer() && sameElem(fFrom.Type, fTo.Type, types.renamed):
			changes = append(changes, FieldChange{Kind: KindPointerWrap, From: fFrom.Name, NewType: fTo.Type})
		case still && fTo.IsPointer() && !fFrom.IsPointer():
			// Became a pointer to a DIFFERENT type (int -> *int64, io.Reader ->
			// *XBody): neither & nor new(x) would type-check.
			changes = append(changes, retypedChange(fFrom, fTo, retype{}))
		case still && incompatibleTypeChange(fFrom.Type, fTo.Type):
			// Field kept its name but its type changed in a way that breaks
			// existing access patterns (e.g. json.RawMessage []byte -> a typed
			// union map). This is a semantic redesign of the access, not a
			// mechanical rewrite, so it is flagged for a human.
			changes = append(changes, FieldChange{
				Kind: KindManual, From: fFrom.Name, NewType: fTo.Type,
				Note: fmt.Sprintf(
					"field type changed from %q to %q; existing access (e.g. json.Unmarshal on a []byte) must be reworked to the target type",
					fFrom.Type, fTo.Type),
			})
		case still:
			if r, ok := types.retypedValue(fFrom.Type, fTo.Type); ok {
				changes = append(changes, retypedChange(fFrom, fTo, r))
			}
			// else unchanged, or a change between struct shapes whose fields all
			// still fit (see retypedValue)
		default:
			// Field is present only on the source side: resolve via the explicit
			// disposition table, the raw-body-collapse classifier, or mark
			// unclassified if nothing covers it.
			changes = append(changes, dispositionChange(sFrom, sTo, fFrom, toByName, dispByFrom, collapsedToRawBody))
		}
	}
	return changes
}

// dispositionChange resolves a vanished source field to its FieldChange: an
// explicit disposition wins; otherwise a raw-body collapse makes it "manual";
// otherwise it is "unclassified" (a bug - the tool refuses to guess).
func dispositionChange(
	sFrom, sTo Struct, fFrom Field, toByName map[string]Field, dispByFrom map[string]FieldDisposition, collapsedToRawBody bool,
) FieldChange {
	d, ok := dispByFrom[fieldDispKey(sFrom.PkgPath, sFrom.Name, fFrom.Name)]
	if !ok {
		if collapsedToRawBody {
			return FieldChange{
				Kind: KindManual, From: fFrom.Name,
				Note: sTo.Name + " collapsed to a raw Body in the target; read this field by decoding Body instead of a direct field access",
			}
		}
		// No human ruling. The tool cannot know rename-vs-remove, so it refuses to
		// guess; the linter turns this into a loud error if the field is used.
		return FieldChange{
			Kind: KindUnclassified, From: fFrom.Name,
			Note: "no field disposition for " + sFrom.Qualified() + "#" + fFrom.Name +
				"; classify it (rename/remove/manual) in the hop's FieldDispositions table",
		}
	}
	switch d.Action {
	case ActionRename:
		newType := fFrom.Type
		if renamed, has := toByName[d.ToField]; has {
			newType = renamed.Type
		}
		ch := FieldChange{Kind: KindRename, From: fFrom.Name, To: d.ToField, NewType: newType}
		if versionAgnosticType(fFrom.Type) != versionAgnosticType(newType) {
			ch.Note = fmt.Sprintf("type changed from %q to %q", fFrom.Type, newType)
		}
		return ch
	case ActionManual:
		return FieldChange{Kind: KindManual, From: fFrom.Name, Note: d.Note}
	default: // ActionRemove
		return FieldChange{Kind: KindRemove, From: fFrom.Name}
	}
}

// incompatibleTypeChange reports whether a field's type changed in a way that
// breaks existing access. It is deliberately narrow: it fires when one side is
// encoding/json.RawMessage (accessed as a []byte, e.g. via json.Unmarshal) and
// the other is not - the SearchResp.Aggregations case, where v5 moved from a raw
// message to a typed union map - so that hazard gets its own guidance. Other
// type changes are classified by retypedValue.
func incompatibleTypeChange(fromType, toType string) bool {
	const raw = "encoding/json.RawMessage"
	fromRaw := fromType == raw
	toRaw := toType == raw
	return fromRaw != toRaw
}

// hopTypes is what field pairing knows about a hop's types beyond the two
// structs it compares.
type hopTypes struct {
	// renamed maps a version-agnostic source type to its target, so a field
	// whose type follows a type rename still compares equal (see sameElem).
	renamed map[string]string
	// from and to are the named structs each version declares, by
	// version-agnostic qualified name, so a field typed with one is compared
	// through its fields (see retypedValue).
	from, to map[string]Struct
}

// namedStructs indexes snap's structs by version-agnostic qualified name.
func namedStructs(snap *Snapshot) map[string]Struct {
	out := make(map[string]Struct, len(snap.Structs))
	for _, s := range snap.Structs {
		out[versionAgnosticType(s.Qualified())] = s
	}
	return out
}

// retype is how a surviving field's type broke. path is the dotted path of the
// inner field that broke when the break is inside a struct shape, and empty
// when the field's own type broke. from and to are the types at path, unset
// for an inner field the target no longer has, which removed marks.
type retype struct {
	path     string
	from, to string
	removed  bool
}

// retypedValue reports whether a surviving field's type changed so that a
// typed value or a read of the old type no longer fits: *int -> int, string ->
// []string, string -> a named string type, int -> int64, X -> map[string]X.
// Types are compared with version segments normalized, interface{} spelled
// any, and the source element type mapped through the hop's type renames.
//
// Two struct shapes under the same pointer, slice and map layers (an anonymous
// struct, or a named struct either version declares) are compared field by
// field: an inner field that vanished or broke the same way breaks a read
// through it, so the field is reported with that inner path. An unexported
// named type with no recorded fields (the opensearchapi.Client sub-clients) is
// left alone, as callers cannot spell it.
//
// An untyped constant can still fit a retyped field (Size: 10 for int ->
// int64), so such a site gets a line it does not strictly need.
func (h hopTypes) retypedValue(fromType, toType string) (retype, bool) {
	return h.retyped(fromType, toType, map[[2]string]struct{}{})
}

// retyped is retypedValue, with seen holding the struct pairs already being
// compared so a self-referencing struct ends the walk.
func (h hopTypes) retyped(fromType, toType string, seen map[[2]string]struct{}) (retype, bool) {
	broken := retype{from: fromType, to: toType}
	fromLayers, fromElem := splitLayers(comparableType(fromType))
	if to, ok := h.renamed[fromElem]; ok {
		fromElem = to
	}
	toLayers, toElem := splitLayers(comparableType(toType))
	if fromLayers != toLayers {
		return broken, true
	}
	if fromElem == toElem {
		return retype{}, false
	}
	fromFields, fromShape := fieldsOf(fromElem, h.from)
	toFields, toShape := fieldsOf(toElem, h.to)
	switch {
	case fromShape == notStruct || toShape == notStruct:
		return broken, true
	case fromShape == structNoFields || toShape == structNoFields:
		return retype{}, false
	}
	pair := [2]string{fromElem, toElem}
	if _, ok := seen[pair]; ok {
		return retype{}, false
	}
	seen[pair] = struct{}{}

	toByName := make(map[string]Field, len(toFields))
	for _, f := range toFields {
		toByName[f.Name] = f
	}
	for _, f := range fromFields {
		t, still := toByName[f.Name]
		if !still {
			return retype{path: f.Name, removed: true}, true
		}
		if r, ok := h.retyped(f.Type, t.Type, seen); ok {
			r.path = strings.TrimSuffix(f.Name+"."+r.path, ".")
			return r, true
		}
	}
	return retype{}, false
}

// comparableType is typ version agnostic, with interface{} spelled any: the two
// spellings are one type, and types.Type.String renders either depending on
// how the source wrote it.
func comparableType(typ string) string {
	return strings.ReplaceAll(versionAgnosticType(typ), "interface{}", "any")
}

// mapKeyPrefix matches a leading map key type ("map[string]").
var mapKeyPrefix = regexp.MustCompile(`^map\[[^\[\]]*\]`)

// splitLayers splits typ into its leading pointer, slice and map layers
// ("[]map[string]*") and the element type under them, in that order.
func splitLayers(typ string) (string, string) {
	var layers string
	for {
		var layer string
		switch {
		case strings.HasPrefix(typ, "*"):
			layer = "*"
		case strings.HasPrefix(typ, "[]"):
			layer = "[]"
		default:
			layer = mapKeyPrefix.FindString(typ)
		}
		if layer == "" {
			return layers, typ
		}
		layers += layer
		typ = typ[len(layer):]
	}
}

// shape is what fieldsOf knows about an element type.
type shape int

const (
	// notStruct is read as a value, so any change of type breaks it.
	notStruct shape = iota
	// structNoFields is only read through its fields and methods, but its
	// fields are not recorded, so a change of type is left alone.
	structNoFields
	// structFields is only read through its fields, which fieldsOf returns.
	structFields
)

// fieldsOf returns the shape of elem, a version-agnostic element type, and its
// fields when they are recorded. An element is struct shaped, only ever read
// through its fields and methods, when it is an anonymous struct, a named
// struct in named (one version's structs), or an unexported named type, which
// callers cannot spell. The fields are not recorded for an unexported type
// outside named, or an anonymous struct that does not parse.
func fieldsOf(elem string, named map[string]Struct) ([]Field, shape) {
	if strings.HasPrefix(elem, "struct{") {
		if fields, ok := anonFields(elem, named); ok {
			return fields, structFields
		}
		return nil, structNoFields
	}
	if s, ok := named[elem]; ok {
		return s.Fields, structFields
	}
	dot := strings.LastIndex(elem, ".")
	if dot < 0 || dot == len(elem)-1 {
		return nil, notStruct
	}
	if r := elem[dot+1]; 'a' <= r && r <= 'z' {
		return nil, structNoFields
	}
	return nil, notStruct
}

// anonFields parses an anonymous struct type as types.Type.String renders it
// (struct{Name T "tag"; Embedded}) into its fields. An embedded named struct in
// named stands for the fields it promotes, as the surface records a named
// struct's. ok is false when typ is not an anonymous struct.
func anonFields(typ string, named map[string]Struct) ([]Field, bool) {
	body, ok := strings.CutPrefix(typ, "struct{")
	if !ok {
		return nil, false
	}
	body, ok = strings.CutSuffix(body, "}")
	if !ok {
		return nil, false
	}
	var fields []Field
	for _, decl := range splitFieldDecls(body) {
		decl = trimTag(decl)
		if name, typ, ok := strings.Cut(decl, " "); ok {
			fields = append(fields, Field{Name: name, Type: typ})
			continue
		}
		// Embedded: named after its type.
		embedded := strings.TrimPrefix(decl, "*")
		if s, ok := named[embedded]; ok {
			fields = append(fields, s.Fields...)
			continue
		}
		fields = append(fields, Field{Name: embedded[strings.LastIndex(embedded, ".")+1:], Type: decl})
	}
	return fields, true
}

// splitFieldDecls splits an anonymous struct body at the "; " between field
// declarations, skipping any inside a nested type or a quoted tag.
func splitFieldDecls(body string) []string {
	var decls []string
	depth, start, inTag := 0, 0, false
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case inTag && c == '\\':
			i++ // the escaped byte cannot end the tag
		case inTag:
			inTag = c != '"'
		case c == '"':
			inTag = true
		case c == '{' || c == '[' || c == '(':
			depth++
		case c == '}' || c == ']' || c == ')':
			depth--
		case c == ';' && depth == 0:
			decls = append(decls, body[start:i])
			i++ // the space after ";"
			start = i + 1
		}
	}
	if start < len(body) {
		decls = append(decls, body[start:])
	}
	return decls
}

// trimTag drops a field declaration's trailing struct tag. types.Type.String
// quotes the tag, so every quote inside it is escaped and the tag starts at
// the last ` "`.
func trimTag(decl string) string {
	if !strings.HasSuffix(decl, `"`) {
		return decl
	}
	i := strings.LastIndex(decl, ` "`)
	if i < 0 {
		return decl
	}
	if _, err := strconv.Unquote(decl[i+1:]); err != nil {
		return decl
	}
	return decl[:i]
}

// migrateByHand ends the note of every manual retype change.
const migrateByHand = "; migrate this use by hand"

// retypedChange is the manual FieldChange for a surviving field whose type
// changed with no mechanical rewrite, as r describes.
func retypedChange(fFrom, fTo Field, r retype) FieldChange {
	note := fmt.Sprintf("field type changed from %q to %q", fFrom.Type, fTo.Type)
	switch {
	case r.removed:
		note = fmt.Sprintf("its field %q was removed", r.path)
	case r.path != "":
		note = fmt.Sprintf("its field %q changed type from %q to %q", r.path, r.from, r.to)
	}
	return FieldChange{Kind: KindManual, From: fFrom.Name, NewType: fTo.Type, Note: note + migrateByHand}
}

// isRawBodyCollapse reports whether a target struct is the "dynamic schema
// captured as raw JSON" shape: a single exported field named Body of type
// encoding/json.RawMessage. Delete/UpdateByQuery responses take this form in v5.
func isRawBodyCollapse(s Struct) bool {
	if len(s.Fields) != 1 {
		return false
	}
	f := s.Fields[0]
	return f.Name == bodyField && f.Type == "encoding/json.RawMessage"
}

// Field and type names of the request-body split, where the target replaced a
// raw Body io.Reader with a typed Body and kept the raw form as BodyReader.
const (
	bodyField       = "Body"
	bodyReaderField = "BodyReader"
	ioReader        = "io.Reader"
)

// isBodyReaderSplit reports whether a source Body io.Reader faces a target with
// a non-io.Reader Body plus a BodyReader io.Reader. The caller's value is the
// same io.Reader either way, so moving it to BodyReader is exact.
func isBodyReaderSplit(fFrom, fTo Field, toByName map[string]Field) bool {
	if fFrom.Name != bodyField || fFrom.Type != ioReader || fTo.Type == ioReader {
		return false
	}
	br, ok := toByName[bodyReaderField]
	return ok && br.Type == ioReader
}

// typeMajorVersionSeg matches the "/vN" module-major-version segment inside a
// types.Type string, where a package path is followed by "/" (sub-package) or
// "." (type name), e.g. ".../opensearch-go/v4.CausedBy".
var typeMajorVersionSeg = regexp.MustCompile(`/v[0-9]+([/.])`)

// versionAgnosticType strips module-major-version segments from a type string,
// the type-string counterpart of versionAgnostic.
func versionAgnosticType(typ string) string {
	return typeMajorVersionSeg.ReplaceAllString(typ, "$1")
}

// sameElem reports whether toType is a pointer to fromType once version
// segments are normalized and fromType is mapped through the hop's type renames
// (ResponseShards -> *ShardStatistics). Only then is wrapping the value in &
// (or new) a correct rewrite.
func sameElem(fromType, toType string, renamedType map[string]string) bool {
	from := versionAgnosticType(fromType)
	if to, ok := renamedType[from]; ok {
		from = to
	}
	return "*"+from == versionAgnosticType(toType)
}
