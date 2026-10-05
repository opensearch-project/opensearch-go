// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/linter"
)

// TestLookupStructReachable proves an outside caller can read the embedded
// surface through linter.LookupStruct.
func TestLookupStructReachable(t *testing.T) {
	t.Parallel()

	const v5api = "github.com/opensearch-project/opensearch-go/v5/opensearchapi"

	tests := []struct {
		name       string
		major      linter.Major
		pkg        string
		structName string
		wantFields []linter.SurfaceField
		wantOK     bool
	}{
		{
			name:       "known struct",
			major:      5,
			pkg:        v5api,
			structName: "CountReq",
			wantFields: []linter.SurfaceField{
				{Name: "Body", Type: "*" + v5api + ".CountBody"},
				{Name: "BodyReader", Type: "io.Reader"},
				{Name: "Header", Type: "net/http.Header"},
				{Name: "Indices", Type: "[]string"},
				{Name: "Params", Type: "*" + v5api + ".CountParams"},
			},
			wantOK: true,
		},
		{
			name:       "unknown struct",
			major:      5,
			pkg:        v5api,
			structName: "NoSuchReq",
		},
		{
			name:       "unknown major",
			major:      99,
			pkg:        v5api,
			structName: "CountReq",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fields, ok := linter.LookupStruct(tc.major, tc.pkg, tc.structName)
			require.Equal(t, tc.wantOK, ok)
			require.Equal(t, tc.wantFields, fields)
		})
	}
}
