// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package build_test

import (
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/internal/build"
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

func TestJSONDiscriminator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		data        string
		key         string
		wantValue   string
		wantPresent bool
		wantErr     bool
	}{
		{name: "string value", data: `{"type":"keyword"}`, key: "type", wantValue: "keyword", wantPresent: true},
		{
			name:      "value among siblings",
			data:      `{"index":true,"type":"boolean","store":false}`,
			key:       "type",
			wantValue: "boolean", wantPresent: true,
		},
		{name: "alternate key", data: `{"mode":"sniff","seeds":["a:9300"]}`, key: "mode", wantValue: "sniff", wantPresent: true},
		{name: "empty string value", data: `{"type":""}`, key: "type", wantValue: "", wantPresent: true},

		// Absent is not an error: the caller falls back to the discriminator's
		// x-default branch, which is how the spec spells an implicit mapping.
		{name: "key absent", data: `{"properties":{}}`, key: "type", wantPresent: false},
		{name: "empty object", data: `{}`, key: "type", wantPresent: false},

		// A non-string value cannot name a branch, and neither can a non-object
		// payload, so both are errors rather than a silent miss.
		{name: "numeric value", data: `{"type":42}`, key: "type", wantPresent: true, wantErr: true},
		{name: "object value", data: `{"type":{"a":1}}`, key: "type", wantPresent: true, wantErr: true},
		{name: "array payload", data: `[1,2,3]`, key: "type", wantErr: true},
		{name: "primitive payload", data: `42`, key: "type", wantErr: true},
		{name: "malformed json", data: `{`, key: "type", wantErr: true},

		// A JSON null unmarshals into a string as the empty string rather than
		// failing, so an explicit null discriminator reads as present-but-empty.
		// No branch is named by "", so the generated decoder reports an unknown
		// discriminator, which is the same outcome as any unrecognized value.
		{name: "null value reads as empty", data: `{"type":null}`, key: "type", wantValue: "", wantPresent: true},

		// A null payload leaves the map untouched, so it is indistinguishable
		// from an object with no such key. The generated decoder handles null
		// before reaching here, so this path is only a guard.
		{name: "null payload reads as absent", data: `null`, key: "type", wantPresent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			value, present, err := build.JSONDiscriminator([]byte(tt.data), tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("JSONDiscriminator(%q, %q) err = nil, want an error", tt.data, tt.key)
				}
				return
			}
			if err != nil {
				t.Fatalf("JSONDiscriminator(%q, %q) err = %v, want nil", tt.data, tt.key, err)
			}
			if present != tt.wantPresent {
				t.Errorf("present = %v, want %v", present, tt.wantPresent)
			}
			if value != tt.wantValue {
				t.Errorf("value = %q, want %q", value, tt.wantValue)
			}
		})
	}
}

// TestJSONDiscriminatorReusesPooledMap covers the buffer reuse: the map is
// pooled across calls, so a stale entry from an earlier payload must not leak
// into a later one that lacks the key.
func TestJSONDiscriminatorReusesPooledMap(t *testing.T) {
	t.Parallel()

	for range 3 {
		value, present, err := build.JSONDiscriminator([]byte(`{"type":"keyword"}`), "type")
		if err != nil || !present || value != "keyword" {
			t.Fatalf("seeded call: got (%q, %v, %v)", value, present, err)
		}

		value, present, err = build.JSONDiscriminator([]byte(`{"properties":{}}`), "type")
		if err != nil {
			t.Fatalf("follow-up call err = %v", err)
		}
		if present {
			t.Errorf("present = true, want false: a pooled entry leaked from the previous payload")
		}
		if value != "" {
			t.Errorf("value = %q, want empty", value)
		}
	}
}
