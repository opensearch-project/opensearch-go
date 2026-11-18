// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
)

type wildcard string

func TestJoinStrings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		in       []wildcard
		expected string
	}{
		{name: "empty", in: nil, expected: ""},
		{name: "one", in: []wildcard{"open"}, expected: "open"},
		{name: "several", in: []wildcard{"open", "hidden", "closed"}, expected: "open,hidden,closed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.expected, apiutil.JoinStrings(tt.in, ","))
		})
	}
}

// A defined string type must cost no more to join than a []string: one
// allocation for the result, none for converting the slice. Not parallel:
// AllocsPerRun counts every allocation in the process, so a concurrent test
// would inflate it.
func TestJoinStringsAllocs(t *testing.T) {
	in := []wildcard{"open", "hidden"}
	require.InDelta(t, 1, testing.AllocsPerRun(100, func() { _ = apiutil.JoinStrings(in, ",") }), 0)
}
