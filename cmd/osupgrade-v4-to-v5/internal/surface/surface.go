// Package surface models the exported struct surface of one or more
// opensearchapi/opensearch packages at a given version, and derives the
// field-level delta between two versions.
//
// The migration engine is type-aware, not name-based: it must know, per struct,
// exactly which fields were renamed, removed, or had their type change to a
// pointer between v4 and v5. Guessing from field-name text alone is unsafe — it
// is what caused a spurious Indices->Index rule to (nearly) corrupt 166 correct
// call sites, because v5 in fact keeps `Indices` on 62 request types.
//
// Struct keys are fully qualified by package import path, because the same
// field name can fan in across multiple types in different packages: e.g.
// EnableMetrics was removed from BOTH opensearch.Config AND
// opensearchtransport.Config. A bare "EnableMetrics" rule would be ambiguous;
// the delta is keyed "<importpath>.<Type>" so each removal is unambiguous.
//
// Pipeline:
//
//	gensurface (v4 modules) -> surface_v4.json   \
//	                                               >- typemap (hand) -> delta -> rewriter
//	gensurface (v5 modules) -> surface_v5.json   /
//
// Surfaces are generated from real package source (go/packages + go/types),
// committed for auditability, and diffed under a hand-written v4->v5 type-name
// map.
package surface

// Field is one exported struct field as seen by the type checker.
type Field struct {
	Name      string `json:"name"`
	Type      string `json:"type"`      // types.Type.String()
	IsPointer bool   `json:"isPointer"` // true if the field's type is a pointer
	JSONTag   string `json:"jsonTag,omitempty"`
}

// Struct is one exported struct type, qualified by its package import path so
// same-named types in different packages (e.g. opensearch.Config vs
// opensearchtransport.Config) never collide.
type Struct struct {
	PkgPath string  `json:"pkgPath"`
	Name    string  `json:"name"`
	Fields  []Field `json:"fields"`
}

// Qualified returns the "<pkgPath>.<Name>" key used throughout the delta.
func (s *Struct) Qualified() string { return s.PkgPath + "." + s.Name }

// Snapshot is the exported struct surface of one version, spanning every scanned
// package.
type Snapshot struct {
	Version string   `json:"version"` // "v4" or "v5", for provenance
	Structs []Struct `json:"structs"`
}

// byName finds a struct by unqualified name within a specific package path.
func (s *Snapshot) lookup(pkgPath, name string) (Struct, bool) {
	for _, st := range s.Structs {
		if st.PkgPath == pkgPath && st.Name == name {
			return st, true
		}
	}
	return Struct{}, false
}

// lookupVersionAgnostic finds a struct by name whose package path matches
// pkgPath once the module-major-version segment is normalized away, so a v4
// package path pairs with its v5 successor (see versionAgnostic in delta.go).
func (s *Snapshot) lookupVersionAgnostic(pkgPath, name string) (Struct, bool) {
	want := versionAgnostic(pkgPath)
	for _, st := range s.Structs {
		if st.Name == name && versionAgnostic(st.PkgPath) == want {
			return st, true
		}
	}
	return Struct{}, false
}

// Field returns the named field of a struct and whether it was found.
func (s *Struct) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}
