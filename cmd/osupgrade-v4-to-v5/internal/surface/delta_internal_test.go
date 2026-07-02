package surface

import "testing"

// TestVersionAgnostic verifies that the module-major-version segment is
// normalized so v4 and v5 package paths for the same logical package pair up.
// This is what lets same-name survivors (opensearch.Config) match across the
// version bump, and is required for the EnableMetrics fan-in to be found in both
// opensearch.Config and opensearchtransport.Config.
func TestVersionAgnostic(t *testing.T) {
	cases := []struct{ in, want string }{
		{"github.com/opensearch-project/opensearch-go/v4/opensearchapi", "github.com/opensearch-project/opensearch-go/opensearchapi"},
		{"github.com/opensearch-project/opensearch-go/v5/opensearchapi", "github.com/opensearch-project/opensearch-go/opensearchapi"},
		{"github.com/opensearch-project/opensearch-go/v4", "github.com/opensearch-project/opensearch-go"},
		{"github.com/opensearch-project/opensearch-go/v5/opensearchtransport", "github.com/opensearch-project/opensearch-go/opensearchtransport"},
		{"example.com/no/version/here", "example.com/no/version/here"},
	}
	for _, c := range cases {
		if got := versionAgnostic(c.in); got != c.want {
			t.Errorf("versionAgnostic(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// v4 and v5 forms of the same package must be equal after normalization.
	v4 := versionAgnostic("github.com/opensearch-project/opensearch-go/v4/opensearchapi")
	v5 := versionAgnostic("github.com/opensearch-project/opensearch-go/v5/opensearchapi")
	if v4 != v5 {
		t.Errorf("v4 and v5 opensearchapi did not normalize equal: %q vs %q", v4, v5)
	}
}

// TestIncompatibleTypeChange pins the narrow json.RawMessage boundary that flags
// a field as "manual" (the SearchResp.Aggregations []byte -> typed-map case),
// while leaving compatible changes alone.
func TestIncompatibleTypeChange(t *testing.T) {
	raw := "encoding/json.RawMessage"
	typedMap := "map[string]github.com/opensearch-project/opensearch-go/v5/opensearchapi.SearchResultAggregationsValue"
	cases := []struct {
		v4, v5 string
		want   bool
	}{
		{raw, typedMap, true},  // Aggregations: RawMessage -> typed map
		{typedMap, raw, true},  // symmetric
		{"string", "string", false},
		{"int", "int64", false}, // numeric widening is not this hazard
		{raw, raw, false},
	}
	for _, c := range cases {
		if got := incompatibleTypeChange(c.v4, c.v5); got != c.want {
			t.Errorf("incompatibleTypeChange(%q, %q) = %v, want %v", c.v4, c.v5, got, c.want)
		}
	}
}

// TestIsRawBodyCollapse verifies detection of the v5 "dynamic schema captured as
// raw JSON" response shape (a single Body json.RawMessage field), which drives
// the by-query "manual" classification.
func TestIsRawBodyCollapse(t *testing.T) {
	collapsed := Struct{Name: "DeleteByQueryResp", Fields: []Field{
		{Name: "Body", Type: "encoding/json.RawMessage"},
	}}
	if !isRawBodyCollapse(collapsed) {
		t.Error("expected single Body json.RawMessage struct to be a raw-body collapse")
	}
	structured := Struct{Name: "GetResp", Fields: []Field{
		{Name: "Body", Type: "encoding/json.RawMessage"},
		{Name: "Found", Type: "bool"},
	}}
	if isRawBodyCollapse(structured) {
		t.Error("multi-field struct must not be treated as a raw-body collapse")
	}
}
