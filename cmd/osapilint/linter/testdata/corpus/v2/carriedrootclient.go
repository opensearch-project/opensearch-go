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

// indexNames is a helper whose argument is the v2 root client. Using its call as
// a positional means the arg subtree carried into the v3 Req contains a live v2
// root-client reference.
func indexNames(_ *opensearchv2.Client) []string { return []string{"i"} }

// carriedRootClient exercises the descent-stop guard: the Indices.Exists
// positional is indexNames(client), whose subtree embeds a v2 root-client
// reference (client). The rewrite carries positionals verbatim into the v3 Req,
// and the descent-stop would leave that reference un-migrated - so the pass must
// plant an _OSAPILINT_RESOLVE marker instead of emitting non-compiling code with
// no sentinel. The golden shows the marker in the Req position.
func carriedRootClient(ctx context.Context, client *opensearchv2.Client) error {
	resp, err := client.Indices.Exists(
		indexNames(client),
		client.Indices.Exists.WithContext(ctx),
	)
	if err != nil {
		return err
	}
	_ = resp
	return nil
}
