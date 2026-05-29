// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package build_test

import (
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/internal/build"
)

func TestHasJSONKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		keys []string
		want bool
	}{
		{name: "no keys requested", data: `{"a":1}`, keys: nil, want: true},
		{
			name: "scalar values present",
			data: `{"max_bytes_behind":0,"total_bytes_behind":0,"max_replication_lag":0}`,
			keys: []string{"max_bytes_behind", "max_replication_lag", "total_bytes_behind"},
			want: true,
		},
		{name: "string values present", data: `{"a":"x","b":"y"}`, keys: []string{"a", "b"}, want: true},
		{name: "bool and null values present", data: `{"a":true,"b":null}`, keys: []string{"a", "b"}, want: true},
		{name: "nested object value present", data: `{"a":{"deep":1},"b":2}`, keys: []string{"a", "b"}, want: true},
		{name: "array value present", data: `{"a":[1,2,3]}`, keys: []string{"a"}, want: true},
		{name: "missing key", data: `{"a":1}`, keys: []string{"a", "b"}, want: false},
		{name: "array json is not an object", data: `[1,2,3]`, keys: []string{"a"}, want: false},
		{name: "primitive json is not an object", data: `42`, keys: []string{"a"}, want: false},
		{name: "null json is not an object", data: `null`, keys: []string{"a"}, want: false},
		{name: "malformed json", data: `{`, keys: []string{"a"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := build.HasJSONKeys([]byte(tt.data), tt.keys...)
			if got != tt.want {
				t.Errorf("HasJSONKeys(%q, %v) = %v, want %v", tt.data, tt.keys, got, tt.want)
			}
		})
	}
}
