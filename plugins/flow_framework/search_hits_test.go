// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package flow_framework_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/plugins/flow_framework"
)

// TestSearchHitsDecodeTypedSource guards the composition of workflow search hits
// with Hit: each hit keeps the Hit fields and decodes _source as a workflow.
func TestSearchHitsDecodeTypedSource(t *testing.T) {
	t.Parallel()

	body := `{"total": {"value": 1, "relation": "eq"}, "max_score": 1.0,
		"hits": [{"_id": "w1", "_index": ".plugins-flow-framework-templates", "_score": 1.0,
			"_source": {"name": "semantic-search", "last_provisioned_time": 1730000000000}}]}`

	var hits flow_framework.FlowFrameworkCommonHits
	require.NoError(t, json.Unmarshal([]byte(body), &hits))

	require.Len(t, hits.Hits, 1)
	hit := hits.Hits[0]
	require.Equal(t, new("w1"), hit.ID)
	require.NotNil(t, hit.Source)
	require.Equal(t, new("semantic-search"), hit.Source.Name)
	require.Equal(t, new(int64(1730000000000)), hit.Source.LastProvisionedTime)
}
