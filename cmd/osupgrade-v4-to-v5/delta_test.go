package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osupgrade-v4-to-v5/internal/surface"
)

// loadSnapshot reads a committed surface JSON.
func loadSnapshot(t *testing.T, path string) *surface.Snapshot {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var s surface.Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return &s
}

// TestTypeMapAgainstSurfaces enforces that every hand-authored type rename is
// real: the v4 type must exist in the v4 surface, and the v5 target must exist
// in the v5 surface. This is the guard that keeps typemap_v4_to_v5.go from
// drifting from the actual package types (the failure mode that produced a
// spurious Indices->Index rule).
func TestTypeMapAgainstSurfaces(t *testing.T) {
	v4 := loadSnapshot(t, "surface_v4.json")
	v5 := loadSnapshot(t, "surface_v5.json")

	has := func(s *surface.Snapshot, pkg, name string) bool {
		for _, st := range s.Structs {
			if st.PkgPath == pkg && st.Name == name {
				return true
			}
		}
		return false
	}

	for _, r := range typeRenamesV4toV5 {
		if !has(v4, r.V4PkgPath, r.V4Name) {
			t.Errorf("typemap: v4 type %s.%s not found in surface_v4.json (stale entry?)", r.V4PkgPath, r.V4Name)
		}
		if !has(v5, r.V5PkgPath, r.V5Name) {
			t.Errorf("typemap: v5 target %s.%s not found in surface_v5.json (wrong rename?)", r.V5PkgPath, r.V5Name)
		}
		// A rename means the v4 name must NOT survive in v5 under the same
		// package (else it isn't a rename and the entry is misleading).
		if has(v5, r.V5PkgPath, r.V4Name) {
			t.Errorf("typemap: %s still exists in v5 under its v4 name; not a rename", r.V4Name)
		}
	}
}

// TestDeriveDeltaKnownChanges pins the delta the rewriter depends on, computed
// from the committed surfaces. These are the exact v4->v5 changes osv4 (the real
// consumer corpus) must undergo.
func TestDeriveDeltaKnownChanges(t *testing.T) {
	v4 := loadSnapshot(t, "surface_v4.json")
	v5 := loadSnapshot(t, "surface_v5.json")
	d := surface.DeriveDelta(v4, v5, typeRenamesV4toV5)

	// DocumentGetReq -> GetReq carries both a field rename and a pointer-wrap.
	getReq := d.Structs[v4api+".DocumentGetReq"]
	if getReq.V5 != v5api+".GetReq" {
		t.Fatalf("DocumentGetReq should map to %s.GetReq, got %q", v5api, getReq.V5)
	}
	assertChange(t, getReq.Changes, surface.FieldChange{Kind: "rename", From: "DocumentID", To: "ID", NewType: "string"})
	assertChangeKind(t, getReq.Changes, "Params", "pointerWrap")

	// EnableMetrics fan-in: removed from BOTH Config structs, as distinct keys.
	root := "github.com/opensearch-project/opensearch-go/v4.Config"
	transport := "github.com/opensearch-project/opensearch-go/v4/opensearchtransport.Config"
	assertChangeKind(t, d.Structs[root].Changes, "EnableMetrics", "remove")
	assertChangeKind(t, d.Structs[transport].Changes, "EnableMetrics", "remove")
}

func assertChange(t *testing.T, changes []surface.FieldChange, want surface.FieldChange) {
	t.Helper()
	for _, c := range changes {
		if c == want {
			return
		}
	}
	t.Errorf("missing expected change %+v in %+v", want, changes)
}

func assertChangeKind(t *testing.T, changes []surface.FieldChange, field, kind string) {
	t.Helper()
	for _, c := range changes {
		if c.From == field && c.Kind == kind {
			return
		}
	}
	t.Errorf("missing %s change for field %q in %+v", kind, field, changes)
}
