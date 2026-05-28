// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

func TestNodeKey(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }

	tests := []struct {
		name string
		rec  opensearchapi.CatNodesRecord
		want string
	}{
		{
			name: "id wins when present",
			rec:  opensearchapi.CatNodesRecord{ID: ptr("g0NX5mUZSdqtKV"), Name: ptr("node-1"), IP: ptr("10.0.0.1")},
			want: "g0NX5mUZSdqtKV",
		},
		{
			name: "name+ip composite when id missing",
			rec:  opensearchapi.CatNodesRecord{Name: ptr("data"), IP: ptr("10.0.0.1")},
			want: "data@10.0.0.1",
		},
		{
			name: "two nodes sharing name disambiguate via ip",
			rec:  opensearchapi.CatNodesRecord{Name: ptr("data"), IP: ptr("10.0.0.2")},
			want: "data@10.0.0.2",
		},
		{
			name: "ip alone when name missing",
			rec:  opensearchapi.CatNodesRecord{IP: ptr("10.0.0.3")},
			want: "10.0.0.3",
		},
		{
			name: "name alone when ip missing",
			rec:  opensearchapi.CatNodesRecord{Name: ptr("orphan")},
			want: "orphan",
		},
		{
			name: "empty record yields empty key",
			rec:  opensearchapi.CatNodesRecord{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, nodeKey(tt.rec))
		})
	}
}

func TestNodeKey_DistinguishesSameNamedNodes(t *testing.T) {
	t.Parallel()

	ptr := func(s string) *string { return &s }
	a := opensearchapi.CatNodesRecord{Name: ptr("data"), IP: ptr("10.0.0.1")}
	b := opensearchapi.CatNodesRecord{Name: ptr("data"), IP: ptr("10.0.0.2")}

	require.NotEqual(t, nodeKey(a), nodeKey(b),
		"two nodes with the same name on different IPs must produce distinct FSM keys")
}
