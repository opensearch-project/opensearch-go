// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package corpus

import (
	"context"
	"fmt"

	opensearchv2 "github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// seedOps exercises the two idiom-2 seed ops the v2->v3 rewrite handles: a
// WithContext-only Ping and Indices.Exists, plus a Status() read in an error
// path. The .golden sibling is the expected v3 output.
func seedOps(ctx context.Context, client *opensearchv2.Client, idx []string) error {
	resp, err := client.Ping(client.Ping.WithContext(ctx))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return fmt.Errorf("ping failed: %s", resp.Status())
	}

	existsResp, err := client.Indices.Exists(idx, client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	if existsResp.IsError() {
		return fmt.Errorf("exists failed: code %d", existsResp.StatusCode)
	}
	return nil
}

// newClient exercises the client-lifecycle reshape: a flat v2 Config literal and
// NewClient that the rewrite wraps into opensearchapi.Config + opensearchapi.NewClient.
func newClient(addrs []string) (*opensearchv2.Client, error) {
	cfg := opensearchv2.Config{Addresses: addrs}
	cfg.Username = "u"
	cfg.Password = "p"
	return opensearchv2.NewClient(cfg)
}

var _ = opensearchapi.PingRequest{}
