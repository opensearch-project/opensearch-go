// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchutil

import (
	"context"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// MGet fetches req's documents with [opensearchapi.MGetEngineDirect], whatever
// the client's configuration and OPENSEARCH_GO_MGET select.
func MGet(ctx context.Context, client *opensearchapi.Client, req opensearchapi.MGetReq) (*opensearchapi.MGetResp, error) {
	return client.Doc.MGet(opensearchapi.WithMGetEngine(ctx, opensearchapi.MGetEngineDirect), req)
}
