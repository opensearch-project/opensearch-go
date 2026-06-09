// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5"
)

func TestIsPermanentAuthErr_TypedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain transport error", errors.New("connection refused"), false},
		{"hostname containing 401", errors.New("dial tcp host401:9200: timeout"), false},
		{"port number containing 401", errors.New("dial tcp host:4012: connection refused"), false},
		{"body starting with U", errors.New("decode: invalid character 'U' looking for beginning of value"), false},

		{"StringError 401", opensearch.StringError{Status: http.StatusUnauthorized, Err: "denied"}, true},
		{"StringError 403", opensearch.StringError{Status: http.StatusForbidden, Err: "denied"}, true},
		{"StringError 500", opensearch.StringError{Status: http.StatusInternalServerError, Err: "boom"}, false},

		{"StructError 401", opensearch.StructError{Status: http.StatusUnauthorized}, true},
		{"StructError 403", opensearch.StructError{Status: http.StatusForbidden}, true},
		{"StructError 500", opensearch.StructError{Status: http.StatusInternalServerError}, false},

		{"ReasonError 401", opensearch.ReasonError{Status: "401"}, true},
		{"ReasonError 403", opensearch.ReasonError{Status: "403"}, true},
		{"ReasonError 500", opensearch.ReasonError{Status: "500"}, false},

		{"MessageError 401", opensearch.MessageError{Status: "401"}, true},
		{"MessageError 403", opensearch.MessageError{Status: "403"}, true},
		{"MessageError 500", opensearch.MessageError{Status: "500"}, false},

		{"wrapped StringError 401", fmt.Errorf("query failed: %w", opensearch.StringError{Status: http.StatusUnauthorized}), true},
		{"wrapped StructError 403", fmt.Errorf("query failed: %w", opensearch.StructError{Status: http.StatusForbidden}), true},
		{"wrapped plain transient error", fmt.Errorf("query failed: %w", errors.New("connection refused")), false},

		{"transport error falls through to readiness substring (Unauthorized)", errors.New("server returned 401 Unauthorized"), true},
		{"transport error falls through to readiness substring (Forbidden)", errors.New("server returned 403 Forbidden"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isPermanentAuthErr(tt.err)
			require.Equal(t, tt.want, got, "isPermanentAuthErr(%v)", tt.err)
		})
	}
}
