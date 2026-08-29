// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLookupStructExported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		major  Major
		pkg    string
		strct  string
		wantOk bool
	}{
		{
			name:   "known struct",
			major:  5,
			pkg:    v5api,
			strct:  "CountReq",
			wantOk: true,
		},
		{
			name:   "unknown struct",
			major:  5,
			pkg:    v5api,
			strct:  "NoSuchReq",
			wantOk: false,
		},
		{
			name:   "unknown major",
			major:  99,
			pkg:    v5api,
			strct:  "CountReq",
			wantOk: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			st, ok := LookupStruct(tc.major, tc.pkg, tc.strct)
			require.Equal(t, tc.wantOk, ok)
			if !tc.wantOk {
				return
			}

			m := make(map[string]string, len(st.Fields))
			for _, f := range st.Fields {
				m[f.Name] = f.Type
			}
			require.Equal(t, "[]string", m["Indices"])
			require.Equal(t, "io.Reader", m["BodyReader"])
		})
	}
}
