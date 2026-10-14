// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func TestBuildMSearchBody(t *testing.T) {
	t.Parallel()

	size := 5
	tests := []struct {
		name        string
		searches    []opensearchapi.MSearchSubSearch
		want        string
		errContains string
	}{
		{
			name:     "empty indices give an empty header",
			searches: []opensearchapi.MSearchSubSearch{{Body: &opensearchapi.SearchBody{Size: &size}}},
			want:     "{}\n{\"size\":5}\n",
		},
		{
			name:     "indices go in the header",
			searches: []opensearchapi.MSearchSubSearch{{Indices: []string{"i1", "i2"}, Body: &opensearchapi.SearchBody{Size: &size}}},
			want:     "{\"index\":[\"i1\",\"i2\"]}\n{\"size\":5}\n",
		},
		{
			name: "order is preserved",
			searches: []opensearchapi.MSearchSubSearch{
				{Indices: []string{"a"}, Body: &opensearchapi.SearchBody{Size: &size}},
				{Indices: []string{"b"}},
			},
			want: "{\"index\":[\"a\"]}\n{\"size\":5}\n{\"index\":[\"b\"]}\n{}\n",
		},
		{
			name:     "non-nil empty indices give an empty header",
			searches: []opensearchapi.MSearchSubSearch{{Indices: []string{}, Body: &opensearchapi.SearchBody{Size: &size}}},
			want:     "{}\n{\"size\":5}\n",
		},
		{
			name:     "date-math index name stays valid JSON",
			searches: []opensearchapi.MSearchSubSearch{{Indices: []string{"<logs-{now/d}>"}}},
			want:     "{\"index\":[\"\\u003clogs-{now/d}\\u003e\"]}\n{}\n",
		},
		{
			name:     "nil body becomes an empty object, not null",
			searches: []opensearchapi.MSearchSubSearch{{}},
			want:     "{}\n{}\n",
		},
		{
			name: "raw JSON containing newlines stays on one line",
			searches: []opensearchapi.MSearchSubSearch{{Body: &opensearchapi.SearchBody{
				Ext: map[string]json.RawMessage{"x": json.RawMessage("{\n  \"a\": 1\n}")},
			}}},
			want: "{}\n{\"ext\":{\"x\":{\"a\":1}}}\n",
		},
		{
			name:        "no sub-searches",
			errContains: "at least one sub-search",
		},
		{
			name: "marshal error names the sub-search",
			searches: []opensearchapi.MSearchSubSearch{
				{},
				{Body: &opensearchapi.SearchBody{Ext: map[string]json.RawMessage{"x": json.RawMessage("{")}}},
			},
			errContains: "sub-search 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, err := opensearchapi.BuildMSearchBody(tt.searches...)
			if tt.errContains != "" {
				require.ErrorContains(t, err, tt.errContains)
				require.Nil(t, r)
				return
			}
			require.NoError(t, err)

			got, err := io.ReadAll(r)
			require.NoError(t, err)
			require.Equal(t, tt.want, string(got))
			for line := range strings.SplitSeq(strings.TrimSuffix(string(got), "\n"), "\n") {
				require.True(t, json.Valid([]byte(line)), "line %q", line)
			}
		})
	}
}

func TestBuildMSearchBody_ReplayableReader(t *testing.T) {
	t.Parallel()

	r, err := opensearchapi.BuildMSearchBody(opensearchapi.MSearchSubSearch{})
	require.NoError(t, err)

	br, ok := r.(*bytes.Reader)
	require.True(t, ok, "got %T", r)
	require.Equal(t, 6, br.Len())

	first, err := io.ReadAll(br)
	require.NoError(t, err)
	_, err = br.Seek(0, io.SeekStart)
	require.NoError(t, err)
	second, err := io.ReadAll(br)
	require.NoError(t, err)
	require.Equal(t, first, second)
}
