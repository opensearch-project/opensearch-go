// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildPath(t *testing.T) {
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
			assert.Equal(t, tt.want, buildPath(tt.segments...))
		})
	}
}
