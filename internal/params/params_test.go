// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package params_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/params"
)

func TestEncodeTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       params.TimeoutParams
		expected map[string]string
	}{
		{name: "negative timeout", in: params.TimeoutParams{Timeout: -1}, expected: map[string]string{"timeout": "-1"}},
		{
			name:     "negative cluster manager timeout",
			in:       params.TimeoutParams{ClusterManagerTimeout: -time.Second},
			expected: map[string]string{"cluster_manager_timeout": "-1"},
		},
		{name: "zero is not set", in: params.TimeoutParams{Timeout: 0}, expected: map[string]string{}},
		{name: "positive timeout", in: params.TimeoutParams{Timeout: time.Second}, expected: map[string]string{"timeout": "1000ms"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := map[string]string{}
			params.EncodeTimeout(tt.in, func(k, v string) { got[k] = v })
			require.Equal(t, tt.expected, got)
		})
	}
}
