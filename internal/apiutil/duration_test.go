// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       time.Duration
		expected string
	}{
		{name: "minus one nanosecond", in: -1, expected: "-1"},
		{name: "minus one millisecond", in: -time.Millisecond, expected: "-1"},
		{name: "minus one hour", in: -time.Hour, expected: "-1"},
		{name: "one nanosecond", in: time.Nanosecond, expected: "1nanos"},
		{name: "sub-millisecond", in: 999 * time.Microsecond, expected: "999000nanos"},
		{name: "one millisecond", in: time.Millisecond, expected: "1ms"},
		{name: "one second", in: time.Second, expected: "1000ms"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, apiutil.FormatDuration(tt.in))
		})
	}
}
