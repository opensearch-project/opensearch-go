// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import "strings"

const pathSep = "/"

// buildPath constructs a URL path from segments, prefixing each non-empty
// segment with "/". Empty segments are skipped so that callers never produce
// the double-slash "//" that http.NewRequest misparses as an authority.
//
//	buildPath("idx", "_alias", "a1")  → "/idx/_alias/a1"
//	buildPath("", "_alias", "a1")     → "/_alias/a1"
//	buildPath("", "_settings")        → "/_settings"
func buildPath(segments ...string) string {
	var size int
	for _, s := range segments {
		if s != "" {
			size += len(pathSep) + len(s)
		}
	}
	var b strings.Builder
	b.Grow(size)
	for _, s := range segments {
		if s != "" {
			b.WriteString(pathSep)
			b.WriteString(s)
		}
	}
	return b.String()
}
