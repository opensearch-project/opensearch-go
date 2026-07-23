// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import "regexp"

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
//   - "rename":      field key/name changed 1:1 (safe to rewrite).
//   - "pointerWrap": field's value type became a pointer (wrap literal in &).
//   - "remove":      field ceased to exist as a settable knob; dropping a
//     composite-literal key for it is correct (e.g. EnableMetrics). NEVER
//     applied to a field access - only literal keys. Only emitted when an
//     explicit FieldDisposition classifies the removal.
//   - "manual":      field vanished but its data/behavior relocated (e.g. a v5
//     Resp collapsed to a raw Body json.RawMessage, so resp.Deleted must become
//     a decode call). Not mechanically rewritable; reported for a human.
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
	Note    string `json:"note,omitempty"` // guidance for "manual"
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
// when a surviving field became a pointer; for a vanished field, the
// FieldDisposition table's action (rename/remove/manual) if present, else
// "unclassified".
func DeriveDelta(from, to *Snapshot, renames []TypeRename, dispositions []FieldDisposition) Delta {
	// Index explicit renames by qualified source key.
	renameByFrom := map[string]TypeRename{}
	for _, r := range renames {
		renameByFrom[r.FromPkgPath+"."+r.FromName] = r
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
			sTo, ok = to.lookup(r.ToPkgPath, r.ToName)
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
		sd.Changes = diffFields(sFrom, sTo, dispByFrom)
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
func diffFields(sFrom, sTo Struct, dispByFrom map[string]FieldDisposition) []FieldChange {
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
		case still && fTo.IsPointer && !fFrom.IsPointer:
			changes = append(changes, FieldChange{Kind: KindPointerWrap, From: fFrom.Name, NewType: fTo.Type})
		case still && incompatibleTypeChange(fFrom.Type, fTo.Type):
			// Field kept its name but its type changed in a way that breaks
			// existing access patterns (e.g. json.RawMessage []byte -> a typed
			// union map). This is a semantic redesign of the access, not a
			// mechanical rewrite, so it is flagged for a human.
			changes = append(changes, FieldChange{
				Kind: KindManual, From: fFrom.Name, NewType: fTo.Type,
				Note: "field type changed from " + fFrom.Type + " to " + fTo.Type +
					"; existing access (e.g. json.Unmarshal on a []byte) must be reworked to the target type",
			})
		case still:
			// unchanged, or a compatible type change the rewriter need not act on
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
		return FieldChange{Kind: KindRename, From: fFrom.Name, To: d.ToField, NewType: newType}
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
// message to a typed union map. Broader type-equality diffing would be noisy
// (many fields differ only by pointer or package path), so we scope to the known
// hazard.
func incompatibleTypeChange(fromType, toType string) bool {
	const raw = "encoding/json.RawMessage"
	fromRaw := fromType == raw
	toRaw := toType == raw
	return fromRaw != toRaw
}

// isRawBodyCollapse reports whether a target struct is the "dynamic schema
// captured as raw JSON" shape: a single exported field named Body of type
// encoding/json.RawMessage. Delete/UpdateByQuery responses take this form in v5.
func isRawBodyCollapse(s Struct) bool {
	if len(s.Fields) != 1 {
		return false
	}
	f := s.Fields[0]
	return f.Name == "Body" && f.Type == "encoding/json.RawMessage"
}
