// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package errmask_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/errmask"
)

func TestErrorMask_Has(t *testing.T) {
	all := errmask.All
	if !all.Has(errmask.BulkItems) {
		t.Fatalf("All.Has(BulkItems) = false, want true")
	}
	if !all.Has(errmask.BulkItems | errmask.SearchShards) {
		t.Fatalf("All.Has(BulkItems|SearchShards) = false, want true")
	}
	if errmask.BulkItems.Has(errmask.SearchShards) {
		t.Fatalf("BulkItems.Has(SearchShards) = true, want false")
	}
	if errmask.Empty.Has(errmask.BulkItems) {
		t.Fatalf("Empty.Has(BulkItems) = true, want false")
	}
}

func TestErrorMask_Aliases(t *testing.T) {
	if errmask.Unknown != errmask.Empty {
		t.Errorf("Unknown must alias Empty: Unknown=%d Empty=%d", errmask.Unknown, errmask.Empty)
	}
	if errmask.None != errmask.Empty {
		t.Errorf("None must alias Empty: None=%d Empty=%d", errmask.None, errmask.Empty)
	}
}

func TestErrorMask_String(t *testing.T) {
	cases := []struct {
		m    errmask.ErrorMask
		want string
	}{
		{errmask.Empty, "empty"},
		{errmask.BulkItems, "bulk_items"},
		{errmask.SearchShards, "search_shards"},
		{errmask.WriteShards, "write_shards"},
		{errmask.BulkItems | errmask.SearchShards, "bulk_items,search_shards"},
		{
			errmask.BulkItems | errmask.SearchShards | errmask.WriteShards | errmask.MultiSearchItems,
			"bulk_items,search_shards,write_shards,multi_search_items",
		},
		{
			errmask.All,
			strings.Join([]string{
				"bulk_items", "search_shards", "write_shards",
				"broadcast_shards", "node_failures", "bulk_by_scroll_failures",
				"task_failures", "multi_search_items", "multi_doc_items",
				"snapshot_create_shard_failures", "snapshot_get_shard_failures",
				"simulate_doc_failures", "rank_eval_failures",
				"ingestion_shard_failures", "pit_node_failures",
			}, ","),
		},
	}
	for _, c := range cases {
		if got := c.m.String(); got != c.want {
			t.Errorf("ErrorMask(%d).String() = %q, want %q", c.m, got, c.want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		base        errmask.ErrorMask
		want        errmask.ErrorMask
		wantUnknown []string // non-nil expected for tokens that fall through to unknown
	}{
		{name: "empty input", in: "", base: errmask.BulkItems, want: errmask.BulkItems},
		{name: "bare snake", in: "bulk_items", base: errmask.Empty, want: errmask.BulkItems},
		{name: "plus snake", in: "+bulk_items", base: errmask.Empty, want: errmask.BulkItems},
		{name: "minus snake", in: "-bulk_items", base: errmask.All, want: errmask.All &^ errmask.BulkItems},
		{name: "all", in: "+all", base: errmask.Empty, want: errmask.All},
		{name: "all minus search_shards", in: "+all,-search_shards", base: errmask.Empty, want: errmask.All &^ errmask.SearchShards},
		{name: "two bare", in: "bulk_items,search_shards", base: errmask.Empty, want: errmask.BulkItems | errmask.SearchShards},
		{name: "empty token clears", in: "empty", base: errmask.All, want: errmask.Empty},
		{name: "none alias clears", in: "none", base: errmask.All, want: errmask.Empty},
		{name: "unknown alias clears", in: "unknown", base: errmask.All, want: errmask.Empty},
		{name: "empty then add", in: "empty,+search_shards", base: errmask.All, want: errmask.SearchShards},
		{
			name: "whitespace",
			in:   "  +bulk_items ,  -write_shards ",
			base: errmask.All,
			want: errmask.All&^errmask.WriteShards | errmask.BulkItems,
		},
		{
			name: "empty token between commas",
			in:   "+bulk_items,,+search_shards",
			base: errmask.Empty,
			want: errmask.BulkItems | errmask.SearchShards,
		},
		// Only lowercase snake_case is accepted. Uppercase, mixed-case,
		// and PascalCase forms all fall through to "unknown" so there's
		// exactly one canonical spelling for incident-response and
		// config files.
		{
			name:        "uppercase rejected",
			in:          "+BULK_ITEMS,+Search_Shards",
			base:        errmask.Empty,
			want:        errmask.Empty,
			wantUnknown: []string{"+BULK_ITEMS", "+Search_Shards"},
		},
		{
			name:        "pascal case rejected",
			in:          "+BulkItems,+SearchShards",
			base:        errmask.Empty,
			want:        errmask.Empty,
			wantUnknown: []string{"+BulkItems", "+SearchShards"},
		},
		{name: "long wrapper name", in: "+snapshot_create_shard_failures", base: errmask.Empty, want: errmask.SnapshotCreateShardFailures},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, unknown := errmask.Parse(c.in, c.base)
			if c.wantUnknown == nil && len(unknown) != 0 {
				t.Errorf("Parse(%q, %v): unexpected unknown tokens %q", c.in, c.base, unknown)
			}
			if c.wantUnknown != nil {
				require.Equal(t, c.wantUnknown, unknown,
					"Parse(%q, %v): unknown tokens mismatch", c.in, c.base)
			}
			if got != c.want {
				t.Errorf("Parse(%q, %v) = %v, want %v", c.in, c.base, got, c.want)
			}
		})
	}
}

func TestParse_UnknownTokensReturnedNotErrored(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		base        errmask.ErrorMask
		wantMask    errmask.ErrorMask
		wantUnknown []string
	}{
		{"single unknown", "garbage", errmask.Empty, errmask.Empty, []string{"garbage"}},
		{"plus unknown", "+bogus", errmask.BulkItems, errmask.BulkItems, []string{"+bogus"}},
		{"minus unknown", "-mystery", errmask.All, errmask.All, []string{"-mystery"}},
		{"known then unknown", "bulk_items,garbage", errmask.Empty, errmask.BulkItems, []string{"garbage"}},
		{"unknown does not stop later valid tokens", "garbage,+search_shards", errmask.Empty, errmask.SearchShards, []string{"garbage"}},
		{"forward-compat: future bit name ignored", "+future_thing,+bulk_items", errmask.Empty, errmask.BulkItems, []string{"+future_thing"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, unknown := errmask.Parse(c.in, c.base)
			if got != c.wantMask {
				t.Errorf("Parse(%q, %v) mask = %v, want %v", c.in, c.base, got, c.wantMask)
			}
			if !reflect.DeepEqual(unknown, c.wantUnknown) {
				t.Errorf("Parse(%q, %v) unknown = %v, want %v", c.in, c.base, unknown, c.wantUnknown)
			}
		})
	}
}

func TestNew(t *testing.T) {
	cases := []struct {
		name string
		bits []errmask.ErrorMask
		want errmask.ErrorMask
	}{
		{name: "no args is Empty", bits: nil, want: errmask.Empty},
		{name: "single bit", bits: []errmask.ErrorMask{errmask.BulkItems}, want: errmask.BulkItems},
		{
			name: "OR-combines multiple bits",
			bits: []errmask.ErrorMask{errmask.BulkItems, errmask.SearchShards, errmask.WriteShards},
			want: errmask.BulkItems | errmask.SearchShards | errmask.WriteShards,
		},
		{name: "All", bits: []errmask.ErrorMask{errmask.All}, want: errmask.All},
		{
			name: "duplicate bits collapse",
			bits: []errmask.ErrorMask{errmask.BulkItems, errmask.BulkItems},
			want: errmask.BulkItems,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := errmask.New(c.bits...)
			require.NotNil(t, m)
			require.Equal(t, c.want, *m)
		})
	}

	t.Run("each call returns a fresh pointer", func(t *testing.T) {
		a := errmask.New(errmask.BulkItems)
		b := errmask.New(errmask.BulkItems)
		require.NotSame(t, a, b)
	})
}

func TestParse_RoundTrip(t *testing.T) {
	masks := []errmask.ErrorMask{
		errmask.Empty,
		errmask.BulkItems,
		errmask.SearchShards,
		errmask.WriteShards,
		errmask.BulkItems | errmask.WriteShards,
		errmask.SnapshotCreateShardFailures | errmask.SnapshotGetShardFailures,
		errmask.All,
	}
	for _, m := range masks {
		s := m.String()
		got, unknown := errmask.Parse(s, errmask.Empty)
		if len(unknown) != 0 {
			t.Errorf("Parse(%q): unexpected unknown tokens %q", s, unknown)
		}
		if got != m {
			t.Errorf("round trip Parse(%q.String()) = %v, want %v", s, got, m)
		}
	}
}
