// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package corpus

import (
	"context"
	"fmt"
	"strings"

	opensearchv2 "github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

// bulkIdiom1 exercises the idiom-1 (function API) path: a removed-in-v3 request
// type used via .Do(ctx, client). The rewrite bumps the import and reports the
// removed type as MANUAL; the call/response shape is not rewritten (report-only),
// so there is no .golden sibling for this file.
func bulkIdiom1(ctx context.Context, client *opensearchv2.Client, body string) error {
	resp, err := opensearchapi.BulkRequest{
		Index: "docs",
		Body:  strings.NewReader(body),
	}.Do(ctx, client)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.IsError() {
		return fmt.Errorf("bulk failed: %d", resp.StatusCode)
	}
	return nil
}
