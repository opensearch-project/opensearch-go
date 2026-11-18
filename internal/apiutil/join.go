// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil

import "strings"

// JoinStrings is strings.Join for a slice of any string type, so a list query
// parameter of a defined enum type (such as []ExpandWildcard) is joined without
// first being copied into a []string.
func JoinStrings[S ~string](elems []S, sep string) string {
	switch len(elems) {
	case 0:
		return ""
	case 1:
		return string(elems[0])
	}
	n := len(sep) * (len(elems) - 1)
	for _, e := range elems {
		n += len(e)
	}
	var b strings.Builder
	b.Grow(n)
	b.WriteString(string(elems[0]))
	for _, e := range elems[1:] {
		b.WriteString(sep)
		b.WriteString(string(e))
	}
	return b.String()
}
