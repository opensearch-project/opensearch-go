// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

func TestIndicesShardStoresShardStoreException_Unmarshal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantType  string
		wantIndex string
		wantUUID  string
		wantShard string
	}{
		{
			name: "full exception with index metadata",
			input: `{
				"type": "corrupt_index_exception",
				"reason": "failed engine (reason: [corrupt file (source: [flush])]) (resource=preexisting_corruption)",
				"index": "test-indices-shard-stores-123",
				"index_uuid": "abc123-def456",
				"shard": "0"
			}`,
			wantType:  "corrupt_index_exception",
			wantIndex: "test-indices-shard-stores-123",
			wantUUID:  "abc123-def456",
			wantShard: "0",
		},
		{
			name: "minimal exception without metadata",
			input: `{
				"type": "index_shard_closed_exception",
				"reason": "CurrentState[CLOSED] operations only allowed when started/recovering"
			}`,
			wantType: "index_shard_closed_exception",
		},
		{
			name: "exception with caused_by chain",
			input: `{
				"type": "corrupt_index_exception",
				"reason": "failed engine",
				"index": "my-index",
				"index_uuid": "xyz789",
				"shard": "2",
				"caused_by": {
					"type": "i_o_exception",
					"reason": "corrupt file (source: [flush])"
				}
			}`,
			wantType:  "corrupt_index_exception",
			wantIndex: "my-index",
			wantUUID:  "xyz789",
			wantShard: "2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var exc opensearchapi.IndicesShardStoresShardStoreException
			require.NoError(t, json.Unmarshal([]byte(tt.input), &exc))

			require.Equal(t, tt.wantType, exc.Type)

			if tt.wantIndex != "" {
				require.NotNil(t, exc.Index, "expected index field to be present")
				require.Equal(t, tt.wantIndex, *exc.Index)
			}
			if tt.wantUUID != "" {
				require.NotNil(t, exc.IndexUUID, "expected index_uuid field to be present")
				require.Equal(t, tt.wantUUID, *exc.IndexUUID)
			}
			if tt.wantShard != "" {
				require.NotNil(t, exc.Shard, "expected shard field to be present")
				require.Equal(t, tt.wantShard, *exc.Shard)
			}
		})
	}
}
