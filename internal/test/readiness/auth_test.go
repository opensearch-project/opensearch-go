// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package readiness_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/test/readiness"
)

func TestIsPermanentAuthErr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain transport error", errors.New("connection refused"), false},
		{"hostname containing 401 (no longer false-positives)", errors.New("dial tcp host401:9200: timeout"), false},
		{"port number containing 401 (no longer false-positives)", errors.New("dial tcp host:4012: connection refused"), false},
		{"body starting with U (no longer false-positives)", errors.New("decode: invalid character 'U' looking for beginning of value"), false},
		{"transport error with Unauthorized substring", errors.New("server returned 401 Unauthorized"), true},
		{"transport error with Forbidden substring", errors.New("server returned 403 Forbidden"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := readiness.IsPermanentAuthErr(tt.err)
			require.Equal(t, tt.want, got, "IsPermanentAuthErr(%v)", tt.err)
		})
	}
}
