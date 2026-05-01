// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWrapLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "empty",
			text: "",
			want: "",
		},
		{
			name: "short line stays on one line",
			text: "Available: >= 2.0.0",
			want: "//\n// Available: >= 2.0.0",
		},
		{
			name: "fits within limit",
			// "Deprecated: >= 2.0.0. Use the new_endpoint API which is better." = 64 chars, fits in 69.
			text: "Deprecated: >= 2.0.0. Use the new_endpoint API which is better.",
			want: "//\n// Deprecated: >= 2.0.0. Use the new_endpoint API which is better.",
		},
		{
			name: "long line wraps at word boundary",
			text: "Availability: >= 1.0.0; <= 3.0.0. This endpoint has been replaced by the newer version which provides better performance and additional features.",
			want: "//\n// Availability: >= 1.0.0; <= 3.0.0. This endpoint has been replaced by\n// the newer version which provides better performance and additional\n// features.",
		},
		{
			name: "single very long word",
			text: "Deprecated: >= 2.0.0. Usethisverylongendpointnamethatcannotbewrappedatanywordboundaryatall instead.",
			want: "//\n// Deprecated: >= 2.0.0.\n// Usethisverylongendpointnamethatcannotbewrappedatanywordboundaryatall\n// instead.",
		},
		{
			name: "whitespace only",
			text: "   \t  ",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := wrapLine(tt.text)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestDeprecComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prefix string
		msg    string
		want   string
	}{
		{
			name:   "empty message",
			prefix: "Deprecated: >= 3.0.0.",
			msg:    "",
			want:   "//\n// Deprecated: >= 3.0.0.",
		},
		{
			name:   "short single-line message",
			prefix: "Deprecated: >= 3.0.0.",
			msg:    "Use search instead.",
			want:   "//\n// Deprecated: >= 3.0.0. Use search instead.",
		},
		{
			name:   "long single-line message wraps",
			prefix: "Availability: >= 1.0.0; <= 3.0.0.",
			msg:    "This endpoint has been superseded by the v2 API which provides improved consistency and better error handling.",
			want:   "//\n// Availability: >= 1.0.0; <= 3.0.0. This endpoint has been superseded\n// by the v2 API which provides improved consistency and better error\n// handling.",
		},
		{
			name:   "multi-line message preserves formatting",
			prefix: "Deprecated: >= 2.4.0.",
			msg:    "Use the replacement API:\n  POST /_plugins/_new/endpoint\n  GET  /_plugins/_new/endpoint/{id}",
			want:   "//\n// Deprecated: >= 2.4.0.\n//\n//\tUse the replacement API:\n//\t  POST /_plugins/_new/endpoint\n//\t  GET  /_plugins/_new/endpoint/{id}",
		},
		{
			name:   "multi-line with blank lines",
			prefix: "Deprecated: >= 2.0.0.",
			msg:    "First paragraph.\n\nSecond paragraph.",
			want:   "//\n// Deprecated: >= 2.0.0.\n//\n//\tFirst paragraph.\n//\n//\tSecond paragraph.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deprecComment(tt.prefix, tt.msg)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCommentWrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "empty", text: "", want: ""},
		{name: "whitespace only", text: "   \t  ", want: ""},
		{name: "single line", text: "Hello world.", want: "//\n// Hello world."},
		{name: "multi line", text: "Line one.\nLine two.", want: "//\n// Line one.\n// Line two."},
		{name: "blank line between", text: "Para one.\n\nPara two.", want: "//\n// Para one.\n//\n// Para two."},
		{name: "trailing whitespace stripped", text: "trailing   \n  next", want: "//\n// trailing\n//   next"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, commentWrap(tt.text))
		})
	}
}

func TestNormalizeSemver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "major only", input: "2", want: "2.0.0"},
		{name: "major.minor", input: "2.4", want: "2.4.0"},
		{name: "full semver", input: "2.4.1", want: "2.4.1"},
		{name: "four parts unchanged", input: "2.4.1.0", want: "2.4.1.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeSemver(tt.input))
		})
	}
}
