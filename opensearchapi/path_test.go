// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearchapi

import (
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		segments []string
		want     string
	}{
		{"all populated", []string{"idx", "_alias", "a1"}, "/idx/_alias/a1"},
		{"first empty", []string{"", "_alias", "a1"}, "/_alias/a1"},
		{"last empty", []string{"idx", "_alias", ""}, "/idx/_alias"},
		{"middle empty", []string{"idx", "", "a1"}, "/idx/a1"},
		{"all empty", []string{"", "", ""}, ""},
		{"single segment", []string{"_settings"}, "/_settings"},
		{"no segments", nil, ""},
		{"comma-joined indices", []string{"i1,i2", "_alias", "a1"}, "/i1,i2/_alias/a1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, buildPath(tt.segments...))
		})
	}
}

func BenchmarkBuildPath(b *testing.B) {
	segments := []string{"my-index", "_alias", "my-alias"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = buildPath(segments...)
	}
}

func BenchmarkPathJoin_Reference(b *testing.B) {
	segments := []string{"my-index", "_alias", "my-alias"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = "/" + strings.TrimPrefix(path.Join(segments...), "/")
	}
}
