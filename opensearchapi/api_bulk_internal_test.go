// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBulkRespItemErrorUnmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		json      string
		wantType  string
		wantError bool
	}{
		{
			name:      "success item without error object",
			json:      `{"_index":"i","_id":"1","status":201}`,
			wantError: false,
		},
		{
			name:      "failed item with error object",
			json:      `{"_index":"i","_id":"2","status":400,"error":{"type":"mapper_parsing_exception","reason":"boom"}}`,
			wantType:  "mapper_parsing_exception",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var item BulkRespItem
			require.NoError(t, json.Unmarshal([]byte(tt.json), &item))
			require.Equal(t, tt.wantError, item.Error != nil)
			if tt.wantError {
				require.Equal(t, tt.wantType, item.Error.Type)
			}
		})
	}
}

func TestBulkRespItemErrorNilPointer(t *testing.T) {
	t.Parallel()

	var item BulkRespItem
	require.Nil(t, item.Error)

	var nilError *BulkRespItemError
	require.Panics(t, func() { _ = nilError.Type })
}
