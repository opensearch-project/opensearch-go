// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package corpus

import (
	"context"

	opensearchv3 "github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

// ping exercises the v3 typed sub-client API. v3 -> v4 is a quiet hop: the
// opensearchapi client and its types are identical across the two versions, so
// the only edit is the import-path bump to v4. The .golden sibling proves the
// rewrite bumps the imports and makes no spurious edits to the call site.
func ping(ctx context.Context, addrs []string) error {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearchv3.Config{Addresses: addrs},
	})
	if err != nil {
		return err
	}
	resp, err := client.Ping(ctx, &opensearchapi.PingReq{})
	if err != nil {
		return err
	}
	if resp.IsError() {
		return context.DeadlineExceeded
	}
	return nil
}
