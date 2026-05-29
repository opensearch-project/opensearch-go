# Error Handling and Partial Failures

> **Note:** Examples in this guide use raw JSON strings for request bodies because the `opensearchapi` package accepts `io.Reader`. When building bodies from user-supplied values, always use `opensearchutil.NewJSONReader` with a Go struct or map instead of string interpolation. See [Security](security.md#request-body-construction) for details.

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

| Operation               | HTTP Status | Partial Failure Indicator | Impact                                    |
| ----------------------- | ----------- | ------------------------- | ----------------------------------------- |
| **Bulk**                | 200         | `errors: true`            | Data loss - some documents not indexed    |
| **Search**              | 200         | `_shards.failed > 0`      | Incomplete results - missing data         |
| **Index/Update/Delete** | 201/200     | `_shards.failed > 0`      | Durability risk - no replica confirmation |
| **Refresh**             | 200         | `_shards.failed > 0`      | Incomplete refresh                        |
| **Cluster operations**  | 200         | `_shards.failed > 0`      | Incomplete stats/operations               |

## Automatic Partial Failure Errors (Recommended)

Configure the client's error mask to control which categories of partial failure surface as typed Go errors. In v4, partial failures are masked by default (preserving pre-bitfield behavior); opt in by setting `Config.Errors: errmask.New()`. In v5+ the default flips and partial failures surface as typed errors automatically.

> **Where wrapper names come from.** Partial-failure categories (`BulkItems`, `SearchShards`, `WriteShards`, ...) are declared by each operation in the OpenAPI spec via the `x-error-responses` extension. The PascalCase wrapper name from the spec is the source of truth. The bit constant on `errmask` matches the spec name verbatim (`errmask.BulkItems`); the env-var token is the lowercase snake_case form (`bulk_items`, `search_shards`, `write_shards`).

```go
mask := errmask.Empty // report every category
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: []string{"https://localhost:9200"}},
    Errors: &mask,
})
```

> **Migration note**: `Config.Errors` is a `*errmask.ErrorMask` pointer. `nil` means "use the version's default": v4 defaults to `errmask.All` (mask everything, preserving pre-bitfield behavior); v5+ defaults to `errmask.Empty` (report every category). Build the pointer with `errmask.New(...)` (the named values are constants, so they are not addressable): `errmask.New()` reports every category, `errmask.New(errmask.All)` masks everything, and `errmask.New(errmask.SearchShards | errmask.MultiSearchItems)` masks specific categories.
>
> The `OPENSEARCH_GO_ERROR_MASK` environment variable can override the value at deploy time. Format is comma-separated `+`/`-` tokens (lowercase snake_case wrapper names like `bulk_items`, `search_shards`). Examples: `OPENSEARCH_GO_ERROR_MASK="+all,-bulk_items"` masks everything except bulk-item errors; `OPENSEARCH_GO_ERROR_MASK="none"` reports everything; `OPENSEARCH_GO_ERROR_MASK="all"` masks everything (mimics the v4 default). Unknown tokens are silently dropped (forward-compatible) and reported via the debug logger when `OPENSEARCH_GO_DEBUG=true`.

### Bulk Operations

When the `BulkItems` bit is unmasked, bulk operations return a `*PartialBulkError` whenever any items fail. The response is still fully populated -- callers can inspect both the error and the response.

```go
resp, err := client.Bulk(ctx, opensearchapi.BulkReq{Body: body})
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialBulkError:
        // resp is fully populated -- inspect individual items
        log.Printf("%d/%d items failed",
            len(e.FailedItems),
            e.SucceededCount+len(e.FailedItems))
        for _, item := range e.FailedItems {
            log.Printf("  %s %s/%s: %s",
                item.Error.Type, item.Index, item.ID, item.Error.Reason)
        }
    default:
        return err // transport or HTTP error
    }
}
```

### Search Operations

Search operations return a `*PartialSearchError` when shards fail. The response contains whatever hits came back from the successful shards.

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Indices: []string{"events"},
    Body:    body,
})
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        log.Printf("%d/%d shards failed, got %d hits",
            e.FailedShards, e.TotalShards,
            len(resp.Hits.Hits))
    default:
        return err
    }
}
```

Multi-search (`MSearch`, `MSearchTemplate`) and scroll (`Scroll.Get`) operations also return `PartialSearchError` when any sub-response has shard failures. The error aggregates failures across all sub-responses.

### Write Operations

Index, Create, Update, and Delete operations return a `*ShardFailureError` when the primary shard succeeds but replica shards fail. The `Operation` field identifies which write operation was performed.

```go
resp, err := client.Index(ctx, opensearchapi.IndexReq{
    Index: "test",
    Body:  strings.NewReader(`{"field": "value"}`),
})
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.ShardFailureError:
        log.Printf("%s: %d/%d shards failed (primary succeeded)",
            e.Operation, e.FailedShards, e.TotalShards)
    default:
        return err
    }
}
```

### Helper Functions

Three helper functions simplify common patterns:

```go
// Test whether an error is a partial failure (any type)
if opensearchapi.IsPartialFailure(err) {
    log.Println("partial failure detected")
}

// Suppress all partial failures -- useful for best-effort operations
err = opensearchapi.ToleratePartialFailures(err)

// Threshold-based tolerance -- fail only if success rate drops below 99%
err = opensearchapi.RequireSuccessRate(err, 0.99)
if err != nil {
    log.Fatal(err) // only reached if <99% succeeded or non-partial error
}
```

### Inspecting Multi-Wrapper Errors with `opensearchapi.Errors`

Operations that declare more than one `x-error-responses` wrapper (today: `MSearch`, `MSearchTemplate`) can fire several sub-errors on the same response. The dispatch handler applies a runtime-collapse rule:

- 0 sub-errors fired: returns `nil`.
- 1 sub-error fired: returns the bare sub-error.
- 2+ sub-errors fired: returns a per-op container (e.g. `*MSearchErrors`) implementing `Unwrap() []error`.

`opensearchapi.Errors(err)` flattens both shapes into a uniform slice so a single switch handles every case. Two idiomatic patterns:

**Inline iteration** -- direct, fewer hops, fine for one-off call sites:

```go
resp, err := client.MSearch(ctx, req)
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        log.Printf("shard agg: %d/%d shards failed", e.FailedShards, e.TotalShards)
    case *opensearchapi.MultiSearchItemError:
        log.Printf("%d sub-queries failed", len(e.Items))
    default:
        // transport / HTTP / decoding error
        return err
    }
}
// resp is fully populated even on partial failure -- continue using it.
```

**Site-specific helper** -- delegate inspection so the call site stays clean. Idiomatic when the same response shape gets handled in several places:

```go
resp, err := client.MSearch(ctx, req)
if err != nil {
    handleMSearchError(err) // log, retry, alert -- whatever your service needs
}
// ... use resp regardless: it is fully populated even on partial failure.

func handleMSearchError(err error) {
    for _, sub := range opensearchapi.Errors(err) {
        switch e := sub.(type) {
        case *opensearchapi.PartialSearchError:
            metrics.ShardFailures.Add(int64(e.FailedShards))
        case *opensearchapi.MultiSearchItemError:
            for _, item := range e.Items {
                log.Printf("sub-query %d failed: %s", item.Index, item.Error.Reason)
            }
        default:
            log.Printf("non-partial msearch error: %v", e)
        }
    }
}
```

`opensearchapi.Errors(nil)` returns `nil`. A non-partial `err` (transport, HTTP, decode) returns a single-element slice containing `err`. Adding a new wrapper category later is purely additive: a new `case` in the switch picks it up; the `default` keeps catching everything else.

### Why a type switch, not `errors.As` or `Has`-style helpers

The wrapper surface is generated from each operation's `x-error-responses` and grows as the spec evolves -- a future OpenSearch Server or OpenSearch API Spec release can add an error category today's call sites have never seen. A type switch over `opensearchapi.Errors(err)` makes that growth visible: static analysis and code review can grep for the switch and flag missing cases, and the `default` arm keeps existing call sites safe in the meantime. `errors.As(err, &target)` and HashiCorp-style helpers (`multierror.Contains`, `errors.Has`) only answer "did _this_ category happen?" -- they cannot tell a call site that a _new_ category appeared and is being silently dropped, because the categories of interest are arguments rather than cases.

Treat `As`/`Has` against OpenAPI-generated error wrappers as an antipattern: every call site that uses them becomes an audit liability the next time the spec adds a category, because the omission is invisible to lint-time checks. The same reasoning applies to operations that today declare a single wrapper -- preferring the type switch from day one means a future spec update is purely additive rather than a silent behavior change.

### Error Type Reference

| Error Type            | Returned By                                                            | Fields (v4 / v5preview)                                                                                             |
| --------------------- | ---------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `*PartialBulkError`   | `Bulk`                                                                 | `FailedItems []BulkRespItem`, `SucceededCount int`                                                                  |
| `*PartialSearchError` | `Search`, `MSearch`, `MSearchTemplate`, `SearchTemplate`, `Scroll.Get` | `FailedShards int`, `TotalShards int`, `Failures []ResponseShardsFailure` (v4) / `[]ShardSearchFailure` (v5preview) |
| `*ShardFailureError`  | `Index`, `Document.Create`, `Document.Delete`, `Update`                | `Operation string`, `FailedShards int`, `TotalShards int`                                                           |

All three implement the `PartialFailureError` interface and work with `errors.As`. Field-name parity is exact across v4 and v5preview; the per-shard failure element type is the only divergence (v5preview uses the spec-driven `ShardSearchFailure` instead of v4's hand-written `ResponseShardsFailure`).

---

## Manual Partial Failure Checking

When the relevant wrapper bits are masked (the v4 default), callers must inspect response fields directly. The sections below document this pattern.

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

### 1. Use Config.Errors to surface partial failures

The simplest way to catch partial failures is to set `Config.Errors` (or the `OPENSEARCH_GO_ERROR_MASK` env var) so the relevant wrapper categories are unmasked. They surface through the standard `error` return, so the idiomatic `if err != nil` catches everything:

```go
mask := errmask.Empty // report every category
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: addrs},
    Errors: &mask,
})

resp, err := client.Bulk(ctx, req)
if err != nil {
    // Catches transport errors, HTTP errors, AND partial failures.
    return err
}
```

### 2. Always Check Partial Failure Indicators

**When the partial-failure wrapper bits are masked**, never assume HTTP 2xx means complete success:

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

### 3. Define Your Error Tolerance

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

With partial-failure errors enabled, use `RequireSuccessRate` for the same effect:

```go
err = opensearchapi.RequireSuccessRate(err, 0.50) // nil unless >50% failed
```

### 4. Implement Retry Logic for Failed Items

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

### 5. Monitor Partial Failure Rates

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

| Error Type                          | Description                        | Retryable? | Action                                                                  |
| ----------------------------------- | ---------------------------------- | ---------- | ----------------------------------------------------------------------- |
| `mapper_parsing_exception`          | Invalid document format            | No         | Fix document                                                            |
| `version_conflict_engine_exception` | Version conflict                   | Maybe      | Retry with updated version                                              |
| `document_missing_exception`        | Document not found (update/delete) | No         | Skip or create                                                          |
| `es_rejected_execution_exception`   | Queue full                         | Yes        | Retry with backoff                                                      |
| `circuit_breaking_exception`        | Circuit breaker tripped            | Yes        | Retry with backoff                                                      |
| `timeout_exception`                 | Operation timeout                  | Yes        | Retry; see [Bulk: Timeout Configuration](bulk.md#timeout-configuration) |

### Shard Failure Reasons

| Reason Type                        | Description           | Action               |
| ---------------------------------- | --------------------- | -------------------- |
| `shard_not_available_exception`    | Shard not ready       | Retry or wait        |
| `primary_missing_action_exception` | Primary shard missing | Check cluster health |
| `search_phase_execution_exception` | Search phase failed   | Review query         |
| `illegal_argument_exception`       | Invalid parameters    | Fix query            |

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

1. **Set `Config.Errors: errmask.New()`** (or unmask specific bits) so partial failures surface through idiomatic `if err != nil` handling.
2. **HTTP 2xx does not guarantee complete success.** With those bits masked, always check partial-failure indicators manually.
3. **Bulk operations**: `PartialBulkError` (or manual `resp.Errors` check) for item-level failures.
4. **Search operations**: `PartialSearchError` (or manual `resp.Shards.Failed` check) for incomplete results.
5. **Write operations**: `ShardFailureError` (or manual `resp.Shards.Failed` check) for replica failures.
6. **Define error tolerance**: Use `RequireSuccessRate` or custom logic to decide what constitutes acceptable failure.
7. **Implement retry logic**: Retry transient failures with backoff.
8. **Monitor failure rates**: Track and alert on partial failures.

Following these practices produces reliable applications that correctly handle OpenSearch's distributed partial-failure model.
