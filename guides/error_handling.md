# Error Handling and Partial Failures

## Overview

OpenSearch is a distributed system in which operations may partially succeed. Understanding how to detect and handle partial failures is essential for building reliable applications.

**Important**: HTTP 2xx status codes do not always indicate complete success. Many operations return HTTP 200 or 201 even when portions of the operation failed.

This guide covers **application-level** partial failure detection: inspecting response bodies for errors that HTTP status codes do not reveal. For **transport-level** retry and connection resurrection (behavior when a node is unreachable), see [retry_backoff.md](retry_backoff.md).

## Understanding OpenSearch's Partial Success Model

### The Problem

OpenSearch returns HTTP 200 or 201 for operations that partially succeed because:

1. **Distributed execution**: Operations execute across multiple shards and nodes.
2. **Availability over consistency**: OpenSearch returns partial results rather than failing entirely.
3. **Best-effort model**: If any part of the operation succeeds, the response indicates success.

This design maximizes availability but requires careful error checking in client code.

### Operations That Can Have Partial Failures

| Operation | HTTP Status | Partial Failure Indicator | Impact |
| --- | --- | --- | --- |
| **Bulk** | 200 | `errors: true` | Data loss - some documents not indexed |
| **Search** | 200 | `_shards.failed > 0` | Incomplete results - missing data |
| **Index/Update/Delete** | 201/200 | `_shards.failed > 0` | Durability risk - no replica confirmation |
| **Refresh** | 200 | `_shards.failed > 0` | Incomplete refresh |
| **Cluster operations** | 200 | `_shards.failed > 0` | Incomplete stats/operations |

## Checking for Partial Failures

### 1. Bulk Operations

Bulk operations always return HTTP 200, even if every individual item failed. When a server-side timeout fires mid-bulk (see [Bulk: Timeout Configuration](bulk.md#timeout-configuration)), the response contains a mix of successful items and items with `timeout_exception` errors -- a partial failure that is invisible to HTTP status code checks alone.

```go
package main

import (
    "context"
    "fmt"
    "log"
    "strings"

    "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func safeBulkOperation(client *opensearchapi.Client, ctx context.Context) error {
    resp, err := client.Bulk(ctx, opensearchapi.BulkReq{
        Body: strings.NewReader(`{ "index": { "_index": "test" } }
{ "field": "value1" }
{ "index": { "_index": "test" } }
{ "field": "value2" }
`),
    })
    if err != nil {
        return fmt.Errorf("bulk request failed: %w", err)
    }

    // CRITICAL: Check the Errors field
    if resp.Errors {
        log.Printf("Bulk operation had partial failures")

        // Examine each item to find failures
        for i, item := range resp.Items {
            for action, details := range item {
                if details.Status >= 300 {
                    log.Printf("Item %d (%s) failed: status=%d, error=%s: %s",
                        i, action, details.Status,
                        details.Error.Type, details.Error.Reason)
                }
            }
        }

        // Decide how to handle - options:
        // 1. Return error if ANY item failed (strict)
        // 2. Return error if MOST items failed (lenient)
        // 3. Continue but log failures (best effort)
        // 4. Retry failed items

        // Example: Fail if more than half failed
        failedCount := 0
        for _, item := range resp.Items {
            for _, details := range item {
                if details.Status >= 300 {
                    failedCount++
                }
            }
        }

        if failedCount > len(resp.Items)/2 {
            return fmt.Errorf("bulk operation mostly failed: %d/%d items",
                failedCount, len(resp.Items))
        }

        // Log but continue
        log.Printf("Bulk completed with %d failures out of %d items",
            failedCount, len(resp.Items))
    }

    return nil
}
```

### 2. Search Operations

Search can return HTTP 200 with incomplete results when some shards fail.

```go
func safeSearchOperation(client *opensearchapi.Client, ctx context.Context) error {
    resp, err := client.Search(ctx, &opensearchapi.SearchReq{
        Indices: []string{"test"},
    })
    if err != nil {
        return fmt.Errorf("search request failed: %w", err)
    }

    // CRITICAL: Check shard failures
    if resp.Shards.Failed > 0 {
        log.Printf("WARNING: Search returned incomplete results")
        log.Printf("Shards: %d total, %d successful, %d failed",
            resp.Shards.Total, resp.Shards.Successful, resp.Shards.Failed)

        // Log failure details
        for _, failure := range resp.Shards.Failures {
            log.Printf("Shard failure: shard=%d, index=%s, reason=%s",
                failure.Shard, failure.Index, failure.Reason.Reason)
        }

        // Calculate failure rate
        failureRate := float64(resp.Shards.Failed) / float64(resp.Shards.Total)

        // Decide how to handle based on failure rate
        if failureRate > 0.5 {
            return fmt.Errorf("too many shard failures (%.0f%%), results unreliable",
                failureRate*100)
        }

        // Continue with partial results but warn user
        log.Printf("Using partial results (%.0f%% of shards succeeded)",
            (1-failureRate)*100)
    }

    // Use search results
    log.Printf("Found %d documents", len(resp.Hits.Hits))
    return nil
}
```

### 3. Index/Update/Delete Operations

Write operations return HTTP 201 or 200 if the primary shard succeeds, even if all replica shards fail.

```go
func safeIndexOperation(client *opensearchapi.Client, ctx context.Context) error {
    resp, err := client.Index(ctx, opensearchapi.IndexReq{
        Index: "test",
        Body:  strings.NewReader(`{"field": "value"}`),
    })
    if err != nil {
        return fmt.Errorf("index request failed: %w", err)
    }

    // CRITICAL: Check replica shard failures
    if resp.Shards.Failed > 0 {
        log.Printf("WARNING: Document indexed but replicas failed")
        log.Printf("Shards: %d total, %d successful, %d failed",
            resp.Shards.Total, resp.Shards.Successful, resp.Shards.Failed)

        // Check durability - is document on primary + at least one replica?
        if resp.Shards.Successful < 2 {
            return fmt.Errorf("durability violation: document only on primary shard")
        }

        log.Printf("Document has reduced durability: only %d/%d replicas confirmed",
            resp.Shards.Successful-1, resp.Shards.Total-1)
    }

    log.Printf("Document indexed: id=%s, version=%d", resp.ID, resp.Version)
    return nil
}
```

## Best Practices

### 1. Always Check Partial Failure Indicators

**Never assume HTTP 2xx means complete success:**

```go
// WRONG - Missing partial failure checks
resp, err := client.Bulk(ctx, req)
if err != nil {
    return err
}
log.Println("Success!") // Danger: resp.Errors may be true

// CORRECT - Check partial failures
resp, err := client.Bulk(ctx, req)
if err != nil {
    return err
}
if resp.Errors {
    // Handle partial failures
}
```

### 2. Define Your Error Tolerance

Different applications have different requirements:

```go
// Strict: Fail if ANY item fails
if resp.Errors {
    return fmt.Errorf("bulk operation incomplete")
}

// Lenient: Fail only if MOST items fail
failedCount := countFailures(resp)
if failedCount > len(resp.Items)*0.5 {
    return fmt.Errorf("too many failures")
}

// Best-effort: Log failures but continue
if resp.Errors {
    log.Printf("Warning: %d items failed", countFailures(resp))
}
```

### 3. Implement Retry Logic for Failed Items

```go
func bulkWithRetry(client *opensearchapi.Client, ctx context.Context, items []string) error {
    maxRetries := 3
    currentItems := items

    for attempt := 0; attempt < maxRetries; attempt++ {
        resp, err := client.Bulk(ctx, buildBulkRequest(currentItems))
        if err != nil {
            return err
        }

        if !resp.Errors {
            return nil // All succeeded
        }

        // Collect failed items for retry
        var failedItems []string
        for i, item := range resp.Items {
            for _, details := range item {
                if details.Status >= 300 {
                    // Check if error is retryable
                    if details.Error != nil && isRetryable(details.Error.Type) {
                        failedItems = append(failedItems, currentItems[i])
                    } else if details.Error != nil {
                        log.Printf("Non-retryable error: %s", details.Error.Type)
                    }
                }
            }
        }

        if len(failedItems) == 0 {
            return nil // All non-retryable errors handled
        }

        log.Printf("Retrying %d failed items (attempt %d/%d)",
            len(failedItems), attempt+1, maxRetries)
        currentItems = failedItems
    }

    return fmt.Errorf("bulk operation failed after %d retries", maxRetries)
}

func isRetryable(errType string) bool {
    // Retry on transient errors
    retryableTypes := map[string]bool{
        "es_rejected_execution_exception": true,
        "circuit_breaking_exception":      true,
        "timeout_exception":                true,
    }
    return retryableTypes[errType]
}
```

### Retrying after context deadline errors requires a server-side timeout

Retrying a bulk request after `context.DeadlineExceeded` is safe only when the original request included a server-side timeout (`BulkParams.Timeout`). Without one, the server likely continues processing the original request after the client gives up. Each retry can add another concurrent bulk operation targeting the same primary shards, potentially exhausting thread pools and triggering `es_rejected_execution_exception` across the cluster. See [Bulk: Why missing server-side timeouts can cause cascading overload](bulk.md#why-missing-server-side-timeouts-can-cause-cascading-overload) for the full failure sequence.

When implementing retry logic for bulk operations:

- **Always set `BulkParams.Timeout`** shorter than the client-side context deadline. This ensures the server aborts incomplete shard operations before the client retries.
- **Do not retry on `context.DeadlineExceeded` if no server-side timeout was set.** The original request is likely still running. Retrying compounds the problem.
- **Retry only the failed items**, not the entire batch. Items that succeeded in the original request do not need to be resubmitted (resubmitting may cause version conflicts or duplicate documents depending on whether document IDs are set).
- **Set client-assigned `_id` values on bulk items.** After a timeout or ambiguous failure, the client can query for expected document IDs to determine which items were persisted, turning a blind retry into a targeted one. See [Bulk: Use client-assigned document IDs for recoverability](bulk.md#use-client-assigned-document-ids-for-recoverability).

### 4. Monitor Partial Failure Rates

```go
type OperationMetrics struct {
    TotalItems       int
    SuccessfulItems  int
    FailedItems      int
    PartialFailures  int
}

func (m *OperationMetrics) Record(resp *opensearchapi.BulkResp) {
    m.TotalItems += len(resp.Items)

    if resp.Errors {
        m.PartialFailures++

        for _, item := range resp.Items {
            for _, details := range item {
                if details.Status < 300 {
                    m.SuccessfulItems++
                } else {
                    m.FailedItems++
                }
            }
        }
    } else {
        m.SuccessfulItems += len(resp.Items)
    }
}

func (m *OperationMetrics) Report() {
    failureRate := float64(m.FailedItems) / float64(m.TotalItems) * 100
    partialRate := float64(m.PartialFailures) / float64(m.TotalItems) * 100

    log.Printf("Metrics: %d total items, %d successful, %d failed (%.2f%% failure rate)",
        m.TotalItems, m.SuccessfulItems, m.FailedItems, failureRate)
    log.Printf("Partial failures: %d operations (%.2f%% of operations had partial failures)",
        m.PartialFailures, partialRate)
}
```

## Common Error Types

### Bulk Operation Errors

| Error Type | Description | Retryable? | Action |
| --- | --- | --- | --- |
| `mapper_parsing_exception` | Invalid document format | No | Fix document |
| `version_conflict_engine_exception` | Version conflict | Maybe | Retry with updated version |
| `document_missing_exception` | Document not found (update/delete) | No | Skip or create |
| `es_rejected_execution_exception` | Queue full | Yes | Retry with backoff |
| `circuit_breaking_exception` | Circuit breaker tripped | Yes | Retry with backoff |
| `timeout_exception` | Operation timeout | Yes | Retry; see [Bulk: Timeout Configuration](bulk.md#timeout-configuration) |

### Shard Failure Reasons

| Reason Type | Description | Action |
| --- | --- | --- |
| `shard_not_available_exception` | Shard not ready | Retry or wait |
| `primary_missing_action_exception` | Primary shard missing | Check cluster health |
| `search_phase_execution_exception` | Search phase failed | Review query |
| `illegal_argument_exception` | Invalid parameters | Fix query |

## Complete Example: Production-Ready Bulk Indexer

```go
package main

import (
    "context"
    "fmt"
    "log"
    "strings"
    "time"

    "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

type BulkIndexer struct {
    client       *opensearchapi.Client
    maxRetries   int
    retryBackoff time.Duration
}

func (b *BulkIndexer) Index(ctx context.Context, docs []Document) error {
    body := buildBulkBody(docs)

    for attempt := 0; attempt < b.maxRetries; attempt++ {
        if attempt > 0 {
            // Exponential backoff, respecting context cancellation
            backoff := b.retryBackoff * time.Duration(1<<uint(attempt-1))
            log.Printf("Retrying after %v (attempt %d/%d)", backoff, attempt+1, b.maxRetries)
            select {
            case <-time.After(backoff):
            case <-ctx.Done():
                return ctx.Err()
            }
        }

        resp, err := b.client.Bulk(ctx, opensearchapi.BulkReq{Body: body})
        if err != nil {
            log.Printf("Bulk request failed: %v", err)
            continue
        }

        // Check for partial failures
        if !resp.Errors {
            log.Printf("Bulk indexing completed successfully: %d documents", len(docs))
            return nil
        }

        // Analyze failures
        var retryableDocs []Document
        permanentFailures := 0

        for i, item := range resp.Items {
            for action, details := range item {
                if details.Status >= 300 {
                    if details.Error != nil && isRetryableError(details.Error.Type) {
                        retryableDocs = append(retryableDocs, docs[i])
                        log.Printf("Retryable failure: item=%d, action=%s, error=%s",
                            i, action, details.Error.Type)
                    } else if details.Error != nil {
                        permanentFailures++
                        log.Printf("Permanent failure: item=%d, action=%s, error=%s: %s",
                            i, action, details.Error.Type, details.Error.Reason)
                    } else {
                        permanentFailures++
                        log.Printf("Permanent failure: item=%d, action=%s, status=%d (no error details)",
                            i, action, details.Status)
                    }
                }
            }
        }

        // Report permanent failures
        if permanentFailures > 0 {
            log.Printf("WARNING: %d documents had permanent failures", permanentFailures)
        }

        // If no retryable failures, we're done
        if len(retryableDocs) == 0 {
            if permanentFailures > 0 {
                return fmt.Errorf("bulk indexing had %d permanent failures", permanentFailures)
            }
            return nil
        }

        // Retry with failed documents
        log.Printf("Will retry %d documents", len(retryableDocs))
        docs = retryableDocs
        body = buildBulkBody(docs)
    }

    return fmt.Errorf("bulk indexing failed after %d retries: %d documents remaining",
        b.maxRetries, len(docs))
}

type Document struct {
    Index string
    ID    string
    Body  map[string]interface{}
}

func buildBulkBody(docs []Document) *strings.Reader {
    var sb strings.Builder
    for _, doc := range docs {
        fmt.Fprintf(&sb, `{"index":{"_index":"%s","_id":"%s"}}%s`,
            doc.Index, doc.ID, "\n")
        // Marshal doc.Body to JSON and append
        sb.WriteString("{}\n") // Simplified
    }
    return strings.NewReader(sb.String())
}

func isRetryableError(errType string) bool {
    retryable := map[string]bool{
        "es_rejected_execution_exception": true,
        "circuit_breaking_exception":      true,
        "timeout_exception":                true,
    }
    return retryable[errType]
}
```

## Summary

1. **HTTP 2xx does not guarantee complete success.** Always check partial failure indicators.
2. **Bulk operations**: Check the `resp.Errors` field and examine each item.
3. **Search operations**: Check `resp.Shards.Failed` for incomplete results.
4. **Write operations**: Check `resp.Shards.Failed` for replica failures.
5. **Define error tolerance**: Determine what constitutes acceptable failure for the application.
6. **Implement retry logic**: Retry transient failures with backoff.
7. **Monitor failure rates**: Track and alert on partial failures.

Following these practices produces reliable applications that correctly handle OpenSearch's distributed partial-failure model.
