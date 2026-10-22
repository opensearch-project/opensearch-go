// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil_test

import "github.com/opensearch-project/opensearch-go/v5/opensearchutil"

// lookupOne returns the first copy of the shard key lives on, and whether router
// knows the index.
func lookupOne(router opensearchutil.ShardRouter, index, key string) (opensearchutil.ShardTarget, bool) {
	var targets []*opensearchutil.ShardTarget
	if err := router.LookupShard(index, key, &targets); err != nil || len(targets) == 0 {
		return opensearchutil.ShardTarget{}, false
	}

	return *targets[0], true
}
