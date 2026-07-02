package surface

import "regexp"

// delta.go derives the field-level migration delta between a v4 struct and its
// v5 counterpart, per fully-qualified type. This is the type-scoped ground truth
// the rewriter applies — it never guesses from a bare field name across a
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
//     applied to a field access — only literal keys.
//   - "manual":      field vanished but its data/behavior relocated (e.g. a v5
//     Resp collapsed to a raw Body json.RawMessage, so resp.Deleted must become
//     a decode call). Not mechanically rewritable; reported for a human.
type FieldChange struct {
	Kind    string `json:"kind"`
	From    string `json:"from"`
	To      string `json:"to,omitempty"`
	NewType string `json:"newType,omitempty"`
	Note    string `json:"note,omitempty"` // guidance for "manual"
}

// StructDelta is the set of field changes for one struct, identified by its
// fully-qualified v4 and v5 type keys ("<pkgPath>.<Name>").
type StructDelta struct {
	V4 string        `json:"v4"` // e.g. ".../v4/opensearchapi.DocumentGetReq"
	V5 string        `json:"v5"` // e.g. ".../v5/opensearchapi.GetReq"
	Changes []FieldChange `json:"changes"`
}

// Delta is the whole v4 -> v5 migration delta, keyed by fully-qualified v4 type.
type Delta struct {
	Structs map[string]StructDelta `json:"structs"`
}

// TypeRename identifies a v4 type that was renamed in v5, by unqualified name
// within a package pair. The engine resolves the v4 struct in v4PkgPath and its
// counterpart in v5PkgPath.
type TypeRename struct {
	V4PkgPath string
	V4Name    string
	V5PkgPath string
	V5Name    string
}

// DeriveDelta diffs every v4 struct against its v5 counterpart and returns the
// per-qualified-type field changes.
//
// Counterpart resolution:
//   - if a TypeRename covers the v4 struct, its v5 name/package are used;
//   - otherwise the v4 struct is paired with the identically named struct in the
//     SAME package path within v5 (same-name survivor).
//
// A v4 struct with no v5 counterpart is skipped (genuinely removed; not
// mechanically migratable). Field pairing within a struct is unchanged from the
// single-package version: pointerWrap when a surviving field became a pointer,
// rename on an unambiguous 1:1 unmatched pair, else remove.
func DeriveDelta(v4, v5 *Snapshot, renames []TypeRename) Delta {
	// Index explicit renames by qualified v4 key.
	renameByV4 := map[string]TypeRename{}
	for _, r := range renames {
		renameByV4[r.V4PkgPath+"."+r.V4Name] = r
	}

	out := Delta{Structs: map[string]StructDelta{}}
	for _, s4 := range v4.Structs {
		var s5 Struct
		var ok bool
		if r, has := renameByV4[s4.Qualified()]; has {
			// explicit rename: resolve by the mapped v5 package + name
			s5, ok = v5.lookup(r.V5PkgPath, r.V5Name)
		} else {
			// same-name survivor: pair by version-agnostic package path + name,
			// because the module major version is baked into the import path
			// (v4/... vs v5/...) and would otherwise never match.
			s5, ok = v5.lookupVersionAgnostic(s4.PkgPath, s4.Name)
		}
		if !ok {
			continue // removed in v5; not mechanically migratable
		}

		sd := StructDelta{V4: s4.Qualified(), V5: s5.Qualified()}
		sd.Changes = diffFields(s4, s5)
		if len(sd.Changes) > 0 {
			out.Structs[s4.Qualified()] = sd
		}
	}
	return out
}

// diffFields computes the field-level changes from a v4 struct to its resolved
// v5 counterpart.
//
// Rename inference is deliberately conservative: a rename is only reported when
// there is EXACTLY ONE unmatched field on each side (a true 1:1). When multiple
// v4 fields vanish (e.g. a v5 Resp collapsed to a single raw Body), they are
// reported as removals, not all "renamed" to the lone survivor — the latter is
// both wrong and dangerous. Multi-field restructurings are surfaced as removals
// for a human to resolve rather than guessed at.
func diffFields(s4, s5 Struct) []FieldChange {
	v5byName := map[string]Field{}
	for _, f := range s5.Fields {
		v5byName[f.Name] = f
	}
	v4Names := map[string]bool{}
	for _, f := range s4.Fields {
		v4Names[f.Name] = true
	}

	// Fields present on only one side.
	var v4Only, v5Only []Field
	for _, f := range s4.Fields {
		if _, ok := v5byName[f.Name]; !ok {
			v4Only = append(v4Only, f)
		}
	}
	for _, f := range s5.Fields {
		if !v4Names[f.Name] {
			v5Only = append(v5Only, f)
		}
	}
	// A rename is only unambiguous at strict 1:1.
	unambiguousRename := len(v4Only) == 1 && len(v5Only) == 1

	// If the v5 counterpart collapsed to a raw catch-all body (a single
	// json.RawMessage field), the vanished fields did not disappear — their data
	// moved behind that Body and now requires a decode. Those are "manual", not
	// safe removals.
	collapsedToRawBody := isRawBodyCollapse(s5)

	var changes []FieldChange
	for _, f4 := range s4.Fields {
		f5, still := v5byName[f4.Name]
		switch {
		case still && f5.IsPointer && !f4.IsPointer:
			changes = append(changes, FieldChange{Kind: "pointerWrap", From: f4.Name, NewType: f5.Type})
		case still && incompatibleTypeChange(f4.Type, f5.Type):
			// Field kept its name but its type changed in a way that breaks
			// existing access patterns (e.g. json.RawMessage []byte -> a typed
			// union map). This is a semantic redesign of the access, not a
			// mechanical rewrite, so it is flagged for a human.
			changes = append(changes, FieldChange{
				Kind: "manual", From: f4.Name, NewType: f5.Type,
				Note: "field type changed from " + f4.Type + " to " + f5.Type +
					"; existing access (e.g. json.Unmarshal on a []byte) must be reworked to the v5 type",
			})
		case still:
			// unchanged, or a compatible type change the rewriter need not act on
		case unambiguousRename:
			changes = append(changes, FieldChange{Kind: "rename", From: f4.Name, To: v5Only[0].Name, NewType: v5Only[0].Type})
		case collapsedToRawBody:
			changes = append(changes, FieldChange{
				Kind: "manual", From: f4.Name,
				Note: s5.Name + " collapsed to a raw Body in v5; read this field by decoding Body (see the wrapper's Decode helper) instead of a direct field access",
			})
		default:
			changes = append(changes, FieldChange{Kind: "remove", From: f4.Name})
		}
	}
	return changes
}

// incompatibleTypeChange reports whether a field's type changed in a way that
// breaks existing access. It is deliberately narrow: it fires when one side is
// encoding/json.RawMessage (accessed as a []byte, e.g. via json.Unmarshal) and
// the other is not — the SearchResp.Aggregations case, where v5 moved from a raw
// message to a typed union map. Broader type-equality diffing would be noisy
// (many fields differ only by pointer or package path), so we scope to the known
// hazard.
func incompatibleTypeChange(v4Type, v5Type string) bool {
	const raw = "encoding/json.RawMessage"
	v4Raw := v4Type == raw
	v5Raw := v5Type == raw
	return v4Raw != v5Raw
}

// isRawBodyCollapse reports whether a v5 struct is the "dynamic schema captured
// as raw JSON" shape: a single exported field named Body of type
// encoding/json.RawMessage. Delete/UpdateByQuery responses take this form in v5.
func isRawBodyCollapse(s Struct) bool {
	if len(s.Fields) != 1 {
		return false
	}
	f := s.Fields[0]
	return f.Name == "Body" && f.Type == "encoding/json.RawMessage"
}
