// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// This sample demonstrates the BulkIndexer as it is meant to be used: one
// long-lived client that you build once, feed jobs of documents, and close once.
// Each job is delimited by a Flush, after which the position it reached in its
// source stream is safe to commit. Along the way it shows client-side document
// ids, a retry policy, an observer, per-item metadata, and a SequenceTracker that
// turns out-of-order completions into a position safe to commit.
//
// Learn more:
//   - Guide: https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-bulk_indexer.md
//   - API reference: https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchutil

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

const (
	// stream names the source the documents are read from, as a Kafka topic and
	// partition would be. The SequenceTracker keeps one position per stream.
	stream = "events/0"
	// firstOffset is the first offset of the stream that this sample reads.
	firstOffset = int64(100)
	// jobs and perJob are how much work the sample does: three jobs of five records.
	jobs, perJob = 3, 5
)

// source is what Metadata carries here: where a document came from. The indexer
// never sends it to the cluster, and hands it back unchanged to the callbacks.
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

// OnRequestEnd runs after every bulk request returns, including a retry.
func (c *counters) OnRequestEnd(context.Context, opensearchutil.BulkRequestEvent) {
	c.requests.Add(1)
}

// OnRequestFailure runs when a whole request fails, once per attempt.
func (c *counters) OnRequestFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	c.requestFailures.Add(1)
	if e.Retrying {
		c.retried.Add(int64(e.Items))
	}
}

// OnItemFailure runs when the cluster rejects one item, once per attempt.
func (c *counters) OnItemFailure(_ context.Context, e opensearchutil.BulkFailureEvent) {
	c.itemFailures.Add(1)
	if e.Retrying {
		c.retried.Add(int64(e.Items))
	}
}

// retryPolicy retries a rejected, unavailable or ambiguous failure up to four
// times, five attempts in all, with exponential backoff: 100ms, 200ms, 400ms,
// 800ms. A permanent failure, such as a mapping error, can never succeed, so it
// is not retried.
// The policy is told whether the whole request failed or a single item did; this
// one treats both the same.
//
// A retry resends the item, so every item needs a stable document id, or a retry
// after an ambiguous failure could index it twice. See fingerprint.
func retryPolicy(
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
}

// fingerprint derives a document id for an item that arrives without one: the
// first 8 bytes of a SHA-256 of the body, which is plenty for a demonstration.
// Two items with identical bodies get the same id, and the second overwrites the
// first, so derive the id from a natural key when your documents have one.
func fingerprint(_ context.Context, item opensearchutil.BulkIndexerItem) (string, error) {
	body, err := io.ReadAll(item.Body)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:8]), nil
}

func main() {
	if err := example(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
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

	// The first write creates the index with dynamic mappings. Delete it when the
	// sample is done, however it ends.
	defer func() {
		_, err := client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
			Indices: []string{index},
			Params:  &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: new(true)},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cleanup: %s\n", err)
		}
	}()

	// Build the indexer once. It is a long-lived client: it keeps a buffer per
	// shard and learns how quickly each node takes writes, so create it when the
	// process starts and reuse it for every job, instead of building one per job.
	obs := &counters{}
	indexer, err := opensearchutil.NewBulkIndexer(opensearchutil.BulkIndexerConfig{
		Client: client,
		Index:  index,

		// A shard's buffer is sent when it is full, and is never larger than
		// FlushBytes. By default the indexer sizes it from write latency, up to this.
		FlushBytes: 5 << 20, // 5 MiB
		// NumWorkers caps the bulk requests the indexer has outstanding at once.
		NumWorkers: 2,

		RetryPolicy:   opensearchutil.RetryPolicyFunc(retryPolicy),
		Fingerprinter: fingerprint,
		Observer:      obs,
	})
	if err != nil {
		return err
	}

	// The tracker reports how far the stream can be committed. Items finish in any
	// order, but it only ever reports the end of a contiguous prefix.
	var tracker opensearchutil.SequenceTracker[string]
	tracker.Begin(stream, firstOffset)

	jobErr := runJobs(ctx, indexer, &tracker)
	if jobErr != nil {
		// Stop the work in flight, so that Close returns promptly.
		indexer.Cancel()
	}
	// Close once, when the indexer is no longer needed. It sends anything still
	// buffered, waits for every item to be final, and stops the indexer's
	// goroutines. Use Cancel instead to stop immediately.
	if err := errors.Join(jobErr, indexer.Close(ctx)); err != nil {
		return err
	}

	stats := indexer.Stats()
	fmt.Printf("added=%d indexed=%d failed=%d\n", stats.NumAdded, stats.NumIndexed, stats.NumFailed)
	fmt.Printf("requests=%d request failures=%d item failures=%d retried=%d\n",
		obs.requests.Load(), obs.requestFailures.Load(), obs.itemFailures.Load(), obs.retried.Load())

	return nil
}

// runJobs feeds the indexer a few jobs of records and commits the position each
// reaches. A job is just the records between two calls to Flush; the indexer
// carries on from job to job without being rebuilt.
func runJobs(ctx context.Context, indexer opensearchutil.BulkIndexer, tracker *opensearchutil.SequenceTracker[string]) error {
	// onSuccess marks the record complete. Every item here carries a source in
	// Metadata, so the type assertion cannot fail.
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

	next := firstOffset
	for job := 1; job <= jobs; job++ {
		for range perJob {
			err := indexer.Add(ctx, opensearchutil.BulkIndexerItem{
				Action:    "index",
				Body:      strings.NewReader(fmt.Sprintf(`{"offset":%d,"title":"Event %d"}`, next, next)),
				Metadata:  source{stream: stream, offset: next},
				OnSuccess: onSuccess,
				OnFailure: onFailure,
			})
			if err != nil {
				return err
			}
			next++
		}

		// The job ends here. Flush returns once every item added so far has had its
		// callback, which says they are final and not that they all succeeded: a
		// failed item shows up in onFailure, and holds the position back.
		if err := indexer.Flush(ctx); err != nil {
			return err
		}

		// Every offset below safe is done, so it can be committed to the stream.
		safe, _ := tracker.Safe(stream)
		fmt.Printf("job %d done: safe to commit up to offset %d\n", job, safe)
	}

	return nil
}
