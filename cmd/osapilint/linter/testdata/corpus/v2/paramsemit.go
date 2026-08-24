// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package corpus

import (
	"context"

	opensearchv2 "github.com/opensearch-project/opensearch-go/v2"
)

// paramsEmit exercises the destParams emit path end-to-end: WithLocal(true) is a
// v2 functional option that maps to the v3 IndicesExistsParams.Local field. The
// golden confirms the rewrite nests it under Req{Params: IndicesExistsParams{...}}
// and wraps the bool in opensearchapi.ToPointer, since v3 Local is a *bool. Only
// a unit test covered this branch before; this pins the whole type-aware emit.
func paramsEmit(ctx context.Context, client *opensearchv2.Client, idx []string) error {
	resp, err := client.Indices.Exists(idx,
		client.Indices.Exists.WithContext(ctx),
		client.Indices.Exists.WithLocal(true),
	)
	if err != nil {
		return err
	}
	_ = resp
	return nil
}
