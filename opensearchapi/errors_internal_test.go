// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi //nolint:testpackage // tests unexported resolveReturnQueryErrors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveReturnQueryErrors(t *testing.T) {
	tests := []struct {
		name     string
		env      *string // nil = unset, non-nil = set to this value
		cfgValue bool
		want     bool
	}{
		{
			name:     "default unset false",
			env:      nil,
			cfgValue: false,
			want:     false,
		},
		{
			name:     "config true no env",
			env:      nil,
			cfgValue: true,
			want:     true,
		},
		{
			name:     "env true overrides config false",
			env:      strPtr("true"),
			cfgValue: false,
			want:     true,
		},
		{
			name:     "env false overrides config true",
			env:      strPtr("false"),
			cfgValue: true,
			want:     false,
		},
		{
			name:     "env 1",
			env:      strPtr("1"),
			cfgValue: false,
			want:     true,
		},
		{
			name:     "env 0 overrides config true",
			env:      strPtr("0"),
			cfgValue: true,
			want:     false,
		},
		{
			name:     "env garbage falls through to config",
			env:      strPtr("maybe"),
			cfgValue: true,
			want:     true,
		},
		{
			name:     "env empty falls through to config",
			env:      strPtr(""),
			cfgValue: false,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != nil {
				t.Setenv(envReturnQueryErrors, *tt.env)
			}

			got := resolveReturnQueryErrors(tt.cfgValue)
			assert.Equal(t, tt.want, got)
		})
	}
}

func strPtr(s string) *string { return &s }
