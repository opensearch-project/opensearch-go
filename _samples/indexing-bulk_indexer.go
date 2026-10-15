// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// This sample demonstrates the BulkIndexer: client-side document ids, a retry
// policy, an observer, per-item metadata, and a SequenceTracker that turns
// out-of-order completions into a position safe to commit.
//
// Learn more:
//   - Guide: https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-bulk_indexer.md
//   - API reference: https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchutil

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
)

// source is what Metadata carries here: where a document came from.
type source struct {
	stream string
	offset int64
}

// counters is a BulkIndexerObserver that counts what the indexer does. Embedding
// the base observer leaves every method it does not override as a no-op.
type counters struct {
	opensearchutil.BaseBulkIndexerObserver

	requests, requestFailures, itemFailures, retried atomic.Int64
}

func (c *counters) OnRequestEnd(context.Context, opensearchutil.BulkRequestEvent) {
	c.requests.Add(1)
}

func (c *counters) OnRequestFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	c.requestFailures.Add(1)
	if e.Retrying {
		c.retried.Add(int64(e.Items))
	}
}

func (c *counters) OnItemFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	c.itemFailures.Add(1)
	if e.Retrying {
		c.retried.Add(int64(e.Items))
	}
}

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	// Initialize the client with SSL/TLS enabled.
	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				InsecureSkipVerify: true, // For testing only. Use certificate for validation.
				Addresses:          []string{"https://localhost:9200"},
				Username:           "admin", // For testing only. Don't store credentials in code.
				Password:           "myStrongPassword123!",
			},
		},
	)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	index := "bulk-indexer-demo"

	// Retry a rejected, unavailable or ambiguous failure up to five times with
	// exponential backoff. A permanent failure, such as a mapping error, can never
	// succeed, so it is not retried. The policy is told whether the whole request
	// failed or a single item did; this one treats both the same.
	retry := opensearchutil.RetryPolicyFunc(func(
		attempt int,
		_ opensearchutil.FailureScope,
		kind opensearchutil.FailureKind,
		_ opensearchapi.BulkRespItem,
		_ error,
	) (time.Duration, bool) {
		if kind == opensearchutil.FailurePermanent || attempt >= 5 {
			return 0, false
		}
		return 100 * time.Millisecond << (attempt - 1), true
	})

	// Retrying needs a stable id for every document, or an item that was retried
	// after an ambiguous failure could be indexed twice. The Fingerprinter derives
	// one for each item that arrives without it: here, a hash of the body.
	fingerprint := func(_ context.Context, item opensearchutil.BulkIndexerItem) (string, error) {
		body, err := io.ReadAll(item.Body)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(body)
		return hex.EncodeToString(sum[:8]), nil
	}

	obs := &counters{}
	indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client:        client,
		Index:         index,
		NumWorkers:    2,
		RetryPolicy:   retry,
		Fingerprinter: fingerprint,
		Observer:      obs,
	})
	if err != nil {
		return err
	}

	// The tracker reports how far the stream can be committed. Items finish in any
	// order, but it only ever reports the end of a contiguous prefix.
	var tracker opensearchutil.SequenceTracker[string]
	const stream, firstOffset = "events/0", int64(100)
	tracker.Begin(stream, firstOffset)

	onSuccess := func(_ context.Context, item opensearchutil.BulkIndexerItem, _ opensearchapi.BulkRespItem) {
		src := item.Metadata.(source)
		tracker.Complete(src.stream, src.offset)
	}
	// A failed item is deliberately not completed: the position stays below it
	// until it is dealt with.
	onFailure := func(_ context.Context, item opensearchutil.BulkIndexerItem, resp opensearchapi.BulkRespItem, err error) {
		src := item.Metadata.(source)
		fmt.Printf("offset %d failed: err=%v status=%d\n", src.offset, err, resp.Status)
	}

	for i := range int64(10) {
		offset := firstOffset + i
		err := indexer.Add(ctx, opensearchutil.BulkIndexerItem{
			Action:    "index",
			Body:      strings.NewReader(fmt.Sprintf(`{"offset":%d,"title":"Event %d"}`, offset, offset)),
			Metadata:  source{stream: stream, offset: offset}, // never sent to the cluster
			OnSuccess: onSuccess,
			OnFailure: onFailure,
		})
		if err != nil {
			return err
		}
	}

	// Flush returns once every item added so far has had its callback.
	if err := indexer.Flush(ctx); err != nil {
		return err
	}
	safe, _ := tracker.Safe(stream)
	fmt.Printf("safe to commit up to offset %d\n", safe)

	// Close waits for anything still in flight. Use Cancel to stop immediately.
	if err := indexer.Close(ctx); err != nil {
		return err
	}

	stats := indexer.Stats()
	fmt.Printf("added=%d indexed=%d failed=%d requests=%d\n",
		stats.NumAdded, stats.NumIndexed, stats.NumFailed, obs.requests.Load())

	// Cleanup.
	_, err = client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
		Indices: []string{index},
		Params:  &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: new(true)},
	})
	return err
}
