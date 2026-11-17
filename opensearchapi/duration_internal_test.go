// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"testing"

	"github.com/stretchr/testify/require"

	osparams "github.com/opensearch-project/opensearch-go/v5/internal/params"
)

func TestDurationUnbounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		params   SearchParams
		key      string
		expected string
	}{
		{name: "scroll", params: SearchParams{Scroll: DurationUnbounded}, key: "scroll", expected: "-1"},
		{
			name:     "embedded timeout",
			params:   SearchParams{TimeoutParams: osparams.TimeoutParams{Timeout: DurationUnbounded}},
			key:      "timeout",
			expected: "-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, tt.params.get()[tt.key])
		})
	}
}
