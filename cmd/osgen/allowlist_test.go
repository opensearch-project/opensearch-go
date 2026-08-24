// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadAllowlist covers the file format both generation guards share. The key
// shape differs between them - "GoType/jsonField" for the json.RawMessage guard,
// "OuterType/jsonTag/DeclaringType" for the duplicate-JSON-tag guard - and the
// cases below mix the two to pin that the parser is key-agnostic: it takes the
// first whitespace-delimited token and never interprets the segments.
func TestLoadAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    []string // keys expected in the set, in any order
	}{
		{
			name:    "keys with comments and blanks",
			content: "# header\n\nSearchHit/_source # bare\n  SearchHit/fields  # map\n\n# trailing\n",
			want:    []string{"SearchHit/_source", "SearchHit/fields"},
		},
		{
			name:    "three-segment keys parse the same way",
			content: "# header\n\nOuter/hits/Base # narrowing\n  Other/x/Base  # narrowing\n",
			want:    []string{"Other/x/Base", "Outer/hits/Base"},
		},
		{
			name:    "group headers ignored",
			content: "# --- search ---\nSearchResp/-\n# --- cat ---\nCatNodesResp/[records]\n",
			want:    []string{"CatNodesResp/[records]", "SearchResp/-"},
		},
		{
			name:    "duplicate keys collapse",
			content: "Dup/k # first\nDup/k # second\nDup/k # third\n",
			want:    []string{"Dup/k"},
		},
		{
			name:    "empty file",
			content: "# only comments\n",
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "allow.txt")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o600))

			allowed, err := loadAllowlist(path, rawMessageNoun, rawMessageUpdateFlag)
			require.NoError(t, err)

			got := make([]string, 0, len(allowed))
			for k := range allowed {
				got = append(got, k)
			}
			require.ElementsMatch(t, tt.want, got)
		})
	}
}

// TestLoadAllowlist_MissingFile pins that each guard's own noun and update flag
// reach the not-found error, so the message names the flag that would create the
// file rather than the other guard's.
func TestLoadAllowlist_MissingFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		noun       string
		updateFlag string
	}{
		{name: "json.RawMessage guard", noun: rawMessageNoun, updateFlag: rawMessageUpdateFlag},
		{name: "duplicate-JSON-tag guard", noun: tagShadowNoun, updateFlag: tagShadowUpdateFlag},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadAllowlist(filepath.Join(t.TempDir(), "does-not-exist.txt"), tt.noun, tt.updateFlag)
			require.Error(t, err)
			require.ErrorContains(t, err, tt.noun)
			require.ErrorContains(t, err, tt.updateFlag)
		})
	}
}
