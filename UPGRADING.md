- [Upgrading to >= 5.0.0](#upgrading-to->=-5.0.0)
  - [Partial failure errors (Config.Errors)](#partial-failure-errors-configerrors)
  - [Response.Body becomes a method](#responsebody-becomes-a-method)
  - [StringError for unknown JSON responses](#stringerror-for-unknown-json-responses)
- [Upgrading to >= 4.7.0](#upgrading-to->=-4.7.0)
  - [opensearch.Request interface signature change](#opensearchrequest-interface-signature-change)
  - [Path segment values are percent-encoded](#path-segment-values-are-percent-encoded)
  - [v5preview/opensearchapi/ package - v5 preview API surface](#v5previewopensearchapi-package---v5-preview-api-surface)
- [Upgrading to >= 4.0.0](#upgrading-to->=-4.0.0)
  - [Import path](#import-path)
  - [Error types](#error-types)
  - [AWS signer](#aws-signer)
  - [Typed failure arrays in by-query and reindex responses](#typed-failure-arrays-in-by-query-and-reindex-responses)
  - [Inline `_shards` structs replaced with ResponseShards](#inline-_shards-structs-replaced-with-responseshards)
  - [`_type` field tags now include omitempty](#_type-field-tags-now-include-omitempty)
  - [Import path](#import-path)
  - [Error types](#error-types)
  - [AWS signer](#aws-signer)
- [Upgrading to >= 3.0.0](#upgrading-to->=-3.0.0)
  - [Client creation](#client-creation)
  - [Requests](#requests)
  - [Responses](#responses)
  - [Error handling](#error-handling)
  - [API reorganization](#api-reorganization)
- [Upgrading to >= 2.3.0](#upgrading-to->=-2.3.0)
  - [Snapshot delete](#snapshot-delete)

# Upgrading OpenSearch Go Client

## Upgrading to >= 5.0.0

### Partial Failure Errors (Config.Errors)

Version 5.0.0 introduces typed partial-failure errors and a per-category bitmask that controls which categories surface as Go errors. OpenSearch returns HTTP 200 for many operations that partially succeed (bulk item failures, shard failures on search, replica failures on writes), so callers historically had to remember a second response inspection after every `if err != nil { ... }`. The new model turns those partial failures into typed errors callers can match on with `errors.As`.

#### Configuring the mask

`Config.Errors` is a `*errmask.ErrorMask` pointer. A set bit suppresses (masks) that category; an unset bit reports it. Three named values cover the common cases:

| Value            | Meaning                                                             |
| ---------------- | ------------------------------------------------------------------- |
| `nil`            | Use the version's default (v4: `errmask.All`; v5+: `errmask.Empty`) |
| `&errmask.Empty` | Mask nothing -- every category is reported as a typed error         |
| `&errmask.All`   | Mask everything -- callers must inspect the response manually       |

`errmask.None` and `errmask.Unknown` are aliases for `errmask.Empty`; all three equal 0. Composite masks (e.g. `errmask.SearchShards | errmask.MultiSearchItems`) suppress specific categories while leaving others reported.

The v4 default (`errmask.All`) preserves pre-bitfield behavior: partial failures are not surfaced as Go errors, so existing v4 code continues to work without modification. Opt in by setting `Config.Errors: &errmask.Empty` (or use `errmask.NewClient`-style helpers). The v5+ default flips to `errmask.Empty` so partial failures surface by default.

```go
mask := errmask.Empty // report every category
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{Addresses: addrs},
    Errors: &mask,
})
```

#### Environment-variable override

`OPENSEARCH_GO_ERROR_MASK` accepts a comma-separated list of `+`/`-` tokens applied left-to-right on top of `Config.Errors`. Tokens are the lowercase snake_case wrapper-schema names from the OpenAPI `x-error-responses` extension (`bulk_items`, `search_shards`, `write_shards`, ...).

```sh
# Mask everything except bulk-item errors (useful with v4: opt out of "mask everything" but suppress search-shard noise)
export OPENSEARCH_GO_ERROR_MASK="+all,-bulk_items"

# Only mask search-shard failures; report every other category
export OPENSEARCH_GO_ERROR_MASK="search_shards"

# Reset to "mask everything" (mimics the v4 default)
export OPENSEARCH_GO_ERROR_MASK="all"

# Reset to "report everything" (the v5+ default)
export OPENSEARCH_GO_ERROR_MASK="none"
```

Unknown tokens are ignored (forward compatible: an older client tolerates new wrapper bits added by a newer release) and reported via the debug logger when `OPENSEARCH_GO_DEBUG` is enabled.

#### Handling typed errors

Each operation returns a typed sub-error per detected wrapper category, and operations declaring multiple `x-error-responses` entries can fire more than one. The dispatch handler applies a runtime-collapse rule:

- 0 sub-errors fired: returns `nil`.
- 1 sub-error fired: returns the bare sub-error (no wrapper allocated).
- 2+ sub-errors fired: returns the per-op error type wrapping the slice.

`errors.As` against a known sub-error type works in **both** the single and multi cases (the per-op type implements `Unwrap() []error`):

```go
resp, err := client.Bulk(ctx, opensearchapi.BulkReq{Body: body})
var bulkErr *opensearchapi.PartialBulkError
if errors.As(err, &bulkErr) {
    log.Printf("%d/%d items failed",
        len(bulkErr.FailedItems),
        bulkErr.SucceededCount+len(bulkErr.FailedItems))
}
```

Callers wanting to enumerate every sub-error from a multi-wrapper op match on the per-op type:

```go
resp, err := client.MSearch(ctx, req)
var msErr *opensearchapi.MsearchErrors
if errors.As(err, &msErr) {
    for _, sub := range msErr.Unwrap() {
        switch e := sub.(type) {
        case *opensearchapi.PartialSearchError: // shard aggregation
        case *opensearchapi.MultiSearchItemError: // per-sub-response Error
        }
    }
}
```

The `opensearchapi.Errors(err) []error` helper flattens the same shape uniformly across single- and multi-wrapper ops, so a single `switch` block handles both:

```go
resp, err := client.MSearch(ctx, req)
for _, sub := range opensearchapi.Errors(err) {
    switch e := sub.(type) {
    case *opensearchapi.PartialSearchError:
        // shard aggregation
    case *opensearchapi.MultiSearchItemError:
        // per-sub-response Error envelope
    default:
        // transport / HTTP / decoding error
    }
}
```

A `nil` `err` returns `nil`; a non-partial err (transport, HTTP, decode) returns a single-element slice containing `err`. Adding new wrapper categories later is purely additive: a new `case` picks it up; the `default` keeps catching everything else.

#### Per-Resp helper methods

Every operation declaring `x-error-responses` exposes per-wrapper helper methods on its typed response, plus a `PartialFailures(mask)` aggregator. Use these when you want focused inspection at the call site without going through the dispatch error:

```go
resp, _ := client.Bulk(ctx, req)
if e := resp.BulkItemFailures(); e != nil {
    log.Printf("%d items failed", len(e.FailedItems))
}

resp2, _ := client.MSearch(ctx, req)
if e := resp2.SearchShardFailures(); e != nil { /* ... */ }
if e := resp2.MultiSearchItemFailures(); e != nil { /* ... */ }
```

The `r.PartialFailures(mask errmask.ErrorMask) []error` aggregator reports every wrapper category not suppressed by `mask` -- useful when reusing the dispatch's mask gating outside the dispatch path.

#### Error types in v4 `opensearchapi/`

| Error Type               | Returned By                                                                                                                         | Key Fields                                                                |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| `*PartialBulkError`      | `Bulk`                                                                                                                              | `FailedItems []BulkRespItem`, `SucceededCount int`                        |
| `*PartialSearchError`    | `Search`, `Scroll.Get`, `SearchTemplate` (single-bit); also via `*MsearchErrors` and `*MsearchTemplateErrors` for shard aggregation | `FailedShards int`, `TotalShards int`, `Failures []ResponseShardsFailure` |
| `*ShardFailureError`     | `Index`, `Document.Create`, `Document.Delete`, `Update`                                                                             | `Operation string`, `FailedShards int`, `TotalShards int`                 |
| `*MultiSearchItemError`  | `MSearch`, `MSearchTemplate` (per-sub-response error inspection)                                                                    | `Items []MultiSearchItemFailure`, `SucceededCount int`                    |
| `*MsearchErrors`         | `MSearch` when 2+ wrappers fire                                                                                                     | `Unwrap() []error` (multi-error contract)                                 |
| `*MsearchTemplateErrors` | `MSearchTemplate` when 2+ wrappers fire                                                                                             | `Unwrap() []error`                                                        |

Per-op `*<Op>Errors` types are the Go 1.20+ multi-error containers; they implement `Unwrap() []error` so `errors.As` against any sub-error type still matches whether the response carried one sub-error or many.

#### Error types in v5preview `v5preview/opensearchapi/` (preview)

The v5preview surface ports the same model, but its error sub-types are spec-driven (regenerated from the OpenAPI spec on every `cmd/osgen` run):

| Field name                      | v4 (`opensearchapi`)    | v5preview (`v5preview/opensearchapi`)      |
| ------------------------------- | ----------------------- | ------------------------------------------ |
| Per-shard failure type          | `ResponseShardsFailure` | `ShardSearchFailure` (spec-driven)         |
| Per-sub-response error envelope | inline `*DocumentError` | embedded `ErrorResponseBase` (spec-driven) |
| Shard envelope type             | `ResponseShards`        | `ShardStatistics` (spec-driven)            |

The v5preview package additionally generates one per-op error type per operation declaring `x-error-responses` (e.g. `*v5preview/opensearchapi.MsearchErrors`, `*v5preview/opensearchapi.MsearchTemplateErrors`). Callers wanting v4-shaped field types should keep using the v4 `opensearchapi` package; v5preview is a preview surface that will become the default in v5.

#### Helper functions

```go
// Suppress all partial failures (best-effort operations)
err = opensearchapi.ToleratePartialFailures(err)

// Fail only if success rate drops below threshold
err = opensearchapi.RequireSuccessRate(err, 0.99)

// Test whether an error is a partial failure
if opensearchapi.IsPartialFailure(err) { ... }
```

#### Operation constants for `ShardFailureError.Operation`

```go
opensearchapi.OperationIndex   // "index"
opensearchapi.OperationCreate  // "create"
opensearchapi.OperationUpdate  // "update"
opensearchapi.OperationDelete  // "delete"
```

See [Error Handling and Partial Failures](guides/error_handling.md) for the full guide.

### StringError for Unknown JSON Responses

Version 5.0.0 returns `*opensearch.StringError` error type instead of `*fmt.wrapError` when response received from the server is an unknown JSON. For example, consider delete document API which returns an unknown JSON body when document is not found.

Before 5.0.0:

```go
docDelResp, err = client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: "movies", DocumentID: "3"})
if err != nil {
	fmt.Println(err)

	if !errors.Is(err, opensearch.ErrJSONUnmarshalBody) && docDelResp != nil {
		resp := docDelResp.Inspect().Response
		// get http status
		fmt.Println(resp.StatusCode)
		body := strings.TrimPrefix(err.Error(), "opensearch error response could not be parsed as error: ")
		errResp := opensearchapi.DocumentDeleteResp{}
		json.Unmarshal([]byte(body), &errResp)
		// extract result field from the body
		fmt.Println(errResp.Result)
	}
}
```

After 5.0.0:

```go
docDelResp, err = client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: "movies", DocumentID: "3"})
if err != nil {
	// parse into *opensearch.StringError
	var myStringErr *opensearch.StringError
	if errors.As(err, &myStringErr) {
		// get http status
		fmt.Println(myStringErr.Status)
		errResp := opensearchapi.DocumentDeleteResp{}
		json.Unmarshal([]byte(myStringErr.Err), &errResp)
		// extract result field from the body
		fmt.Println(errResp.Result)
	}
}
```

### `Response.Body` becomes a method

`Response.Body` changes from a public `io.ReadCloser` field to a `Body()` method. The new `RawBody() []byte` method (available since v4) provides access to the buffered response bytes without consuming the body reader.

Before 5.0.0:

```go
body, err := io.ReadAll(resp.Body)
```

After 5.0.0:

```go
body, err := io.ReadAll(resp.Body())

// Or, if you only need the raw bytes and the response was already read:
raw := resp.RawBody()
```

## Upgrading to >= 4.7.0

### `opensearch.Request` interface signature change

`GetRequest()` now receives the HTTP method from the caller:

```go
// Before
GetRequest() (*http.Request, error)

// After
GetRequest(method string) (*http.Request, error)
```

This change is invisible to almost all callers: the typed `Req` structs that the client consumes (e.g. `opensearchapi.SearchReq`, the v5-preview `opensearchapi.IndexReq`) already implement the new signature. Only code that defines a custom type satisfying `opensearch.Request` is affected. If you maintain such a type, add a `method string` parameter and forward it to your underlying `http.NewRequest` call (or `opensearch.BuildRequest`).

### Path segment values are percent-encoded

Every typed `Req.GetRequest()` method now constructs URL paths through the generated `internal/path/*Path` builders, which unconditionally percent-encode user-supplied segment values (via `url.PathEscape`, plus an explicit `/` -> `%2F` substitution). This closes the [#650] path-injection class of bugs: an `Index` value containing `../../_cluster/health` can no longer escape its segment to alter routing.

Prior to this release, `buildPath` wrote segment values raw, which left the wire format ambiguous and the encoding contract undefined: callers who passed unencoded metacharacters got a malformed URL, and callers who passed pre-encoded values got the encoded bytes through to the server. Both interpretations existed simultaneously. The client now defines a single contract — pass raw, unencoded values; the client encodes — and applies it everywhere.

The practical consequence for callers who were already passing pre-encoded values is that those values are now double-encoded:

```go
// Before: the URL contained "my%2Findex" verbatim (relying on undefined behavior)
// After:  the URL contains "my%252Findex" (the percent itself is encoded)
client.Indices.Get(ctx, opensearchapi.IndicesGetReq{Index: []string{"my%2Findex"}})
```

If your code intentionally passes percent-encoded values, decode them with `url.PathUnescape` before populating the Req struct.

[#650]: https://github.com/opensearch-project/opensearch-go/issues/650

### `v5preview/opensearchapi/` package — v5 preview API surface

This release introduces a new `v5preview/opensearchapi/` package alongside the existing top-level `opensearchapi/` package. The new package is the **preview of the v5 API in the v4 branch** and is generated from the OpenSearch OpenAPI spec by `cmd/osgen`. It deliberately reuses the package name `opensearchapi` so that callers who migrate during the v4 branch only need to change the import path at v5 release time -- every reference in code (e.g. `opensearchapi.IndexReq`, `opensearchapi.NewClient`) stays the same.

**Migration Considerations:**

- Migrating to `v5preview/opensearchapi/` in v4 gives you the v5 surface ahead of v5 release. The trade-off at v5 release time is a single edit per consuming file: change the import path from `/v4/v5preview/opensearchapi` to `/v5/opensearchapi`. Package qualifiers do not change.
- Staying on the top-level `opensearchapi/` package is fine through the rest of v4. At v5, the hand-written `opensearchapi/` is removed; the only forward path is the code-generated API surface (closely matches the existing hand-written ergonomics).

**Import path:**

```go
import "github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"

client, err := opensearchapi.NewClient(opensearchapi.Config{...})
```

**Forward-compatible replace directive:**

To write code today against the eventual v5 import path, add a `replace` directive to your `go.mod` so the `opensearchapi` package resolves to the v5preview:

```
replace github.com/opensearch-project/opensearch-go/v5/opensearchapi => github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi v4.7.0
```

Then write your imports as if v5 already shipped:

```go
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"
```

When v5 ships, drop the `replace` line; nothing else has to change.

**Surface differences worth knowing about:**

- Optional `Params` are `*Params` pointer fields (nil-safe; pass `&opensearchapi.IndexParams{...}` to set).
- Optional boolean query parameters are `*bool` so a deliberate `false` can be sent over the wire.
- Plugin APIs (k-NN, ML, Security, ISM, etc.) live in `v5preview/opensearchapi/plugins/`.

See `v5preview/opensearchapi/README.md` for the full usage guide.

## Upgrading to >= 4.0.0

Version 4.0.0 updated the module import path, moved error types from opensearchapi to opensearch, renamed them, added new error types, and migrated the `signer/aws` package from AWS SDK v1 to AWS SDK v2.

### Import Path

Update all import paths from `v3` to `v4`:

```go
// Before (v3)
import (
    "github.com/opensearch-project/opensearch-go/v3"
    "github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

// After (v4)
import (
    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)
```

Update your `go.mod`:

```bash
go get github.com/opensearch-project/opensearch-go/v4@latest
```

### Error Types

Before 4.0.0: Error types:

- `opensearchapi.Error`
- `opensearchapi.StringError`

With 4.0.0: Error types

- `opensearch.Error` -- base error with string `Err` field
- `opensearch.StringError` -- raw string error body
- `opensearch.ReasonError` -- error with `Reason` and `Status` fields
- `opensearch.MessageError` -- error with `Message` field
- `opensearch.StructError` -- structured JSON error with `Type`, `Reason`, `RootCause` (was `opensearchapi.Error`)

Update `errors.As` targets to use `opensearch.*` instead of `opensearchapi.*`:

```go
// Before (v3)
var opensearchError *opensearchapi.Error
if errors.As(err, &opensearchError) {
    fmt.Println(opensearchError.Err.Type)
}

// After (v4)
var opensearchError *opensearch.StructError
if errors.As(err, &opensearchError) {
    fmt.Println(opensearchError.Err.Type)
}
```

### AWS Signer

The `signer/aws` package now uses AWS SDK v2 instead of AWS SDK v1. AWS SDK v1 reached end-of-support on July 31, 2025.

Before 4.0.0 (AWS SDK v1):

```go
import (
    "github.com/aws/aws-sdk-go/aws/session"
    signer "github.com/opensearch-project/opensearch-go/v3/signer/aws"
)

awsSigner, err := signer.NewSigner(session.Options{
    Config: aws.Config{Region: aws.String("us-east-1")},
})
```

With 4.0.0 (AWS SDK v2):

```go
import (
    "context"
    "github.com/aws/aws-sdk-go-v2/config"
    signer "github.com/opensearch-project/opensearch-go/v4/signer/aws"
)

cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
if err != nil {
    return err
}
awsSigner, err := signer.NewSigner(cfg)
```

The `signer/awsv2` package (which already used AWS SDK v2) remains available at `github.com/opensearch-project/opensearch-go/v4/signer/awsv2` with the same API.

### Typed Failure Arrays in By-Query and Reindex Responses

The `Failures` field in `DocumentDeleteByQueryResp`, `UpdateByQueryResp`, and `ReindexResp` changed from `[]json.RawMessage` to `[]BulkByScrollFailure`.

**Who is affected:** Only callers that directly access the `.Failures` slice. Code that ignores failures or only checks `len(resp.Failures) > 0` compiles without changes.

Before:

```go
for _, raw := range resp.Failures {
    var f MyFailureType
    if err := json.Unmarshal(raw, &f); err != nil { ... }
    // use f.Index, f.Status, etc.
}
```

After:

```go
for _, f := range resp.Failures {
    // Fields are available directly — no unmarshaling needed.
    fmt.Println(f.Index, f.Status, f.Cause)
}
```

### Inline `_shards` Structs Replaced with ResponseShards

The `Shards` field in `IndexResp`, `DocumentCreateResp`, `DocumentDeleteResp`, `UpdateResp`, `IndicesRefreshResp`, and `IndicesCountResp` changed from an anonymous inline struct to the named `ResponseShards` type.

**Who is affected:** Code accessing `resp.Shards.Total`, `resp.Shards.Successful`, or `resp.Shards.Failed` compiles unchanged. The only theoretical break is code using reflection or type assertions on the `Shards` field itself. The new type additionally exposes `Failures []ResponseShardsFailure` and `Skipped int` which were previously unavailable.

### `_type` Field Tags Now Include `omitempty`

All deprecated `_type` fields across response structs now use `json:"_type,omitempty"`. Mapping types were deprecated in Elasticsearch 6.0 (2017), reduced to the single `_doc` type in Elasticsearch 7.0 (2019), and completely removed from the OpenSearch server in 2.0.0 (May 2022). OpenSearch forked from Elasticsearch 7.10.2, so the deprecation was inherited from day one. This change means the empty string is no longer emitted when marshaling response structs back to JSON. Deserialization behavior is unchanged.

## Upgrading to >= 3.0.0

Version 3.0.0 is a major refactor of the client.

### Client Creation

You now create the client from the opensearchapi package instead of opensearch. This was done to make the different APIs independent from each other. Plugin APIs like Security get their own folder and therefore their own sub-lib.

Before 3.0.0:

```go
// default client
client, err := opensearch.NewDefaultClient()

// with config
client, err := opensearch.NewClient(
    opensearch.Config{
	    InsecureSkipVerify: true,
		Addresses: []string{"https://localhost:9200"},
		Username:  "admin",
		Password:  "admin",
	},
)
```

With 3.0.0:

```go
// default client
client, err := opensearchapi.NewDefaultClient()

// with config
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
		Client: opensearch.Config{
			InsecureSkipVerify: true, // For testing only. Use certificate for validation.
			Addresses:          []string{"https://localhost:9200"},
			Username:           "admin", // For testing only. Don't store credentials in code.
			Password:           "admin",
		},
	},
)
```

### Requests

Prior version 3.0.0 there were two options on how to perform requests. You could either use the request struct of the wished function and execute it with the client .Do() function or use the client function and add wanted args with so called With<arg>() functions. With the new version you now use functions attached to the client and give a context and the wanted request body as argument.

Before 3.0.0:

```go
// using the client function and adding args by using the With<arg>() functions
createIndex, err := client.Indices.Create(
    "some-index",
    client.Indices.Create.WithContext(ctx),
    client.Indices.Create.WithBody(strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`)),
)

// using the request struct
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}
createIndexResponse, err := createIndex.Do(ctx, client)
```

With 3.0.0:

```go
createIndexResponse, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
```

### Responses

With the version 3.0.0 the lib no longer returns the opensearch.Response which is just a wrap up http.Response. Instead it will check the response for errors and try to parse the body into existing structs. Please note that some responses are so complex that we parse them as [json.RawMessage](https://pkg.go.dev/encoding/json#RawMessage) so you can parse them to your expected struct. If you need the opensearch.Response, then you can call .Inspect().

Before 3.0.0:

```go
// Create the request
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}
// Execute the requests
resp, err := createIndex.Do(ctx, client)
if err != nil {
	return err
}
// Close the body
defer resp.Body.Close()

// Check if the status code is >299
if resp.IsError() {
	return fmt.Errorf("Opensearch Returned an error: %#v", resp)
}

// Create a struct that represents the create index response
createResp := struct {
	Acknowledged       bool   `json:"acknowledged"`
	ShardsAcknowledged bool   `json:"shards_acknowledged"`
	Index              string `json:"index"`
}{}

// Try to parse the response into the created struct
if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
	return fmt.Errorf("unexpected response body: %d, %#v, %s", resp.StatusCode, resp.Body, err)
}
// Print the created index name
fmt.Println(createResp.Index)
```

With 3.0.0:

```go
// Create and execute the requests
createResp, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
if err != nil {
	return err
}
// Print the created index name
fmt.Println(createResp.Index)

// To get the opensearch.Response/http.Response
rawResp := createResp.Inspect().Response
```

### Error Handling

With opensearch-go >= 3.0.0 opensearchapi responses are now checked for errors. Checking for errors twice is no longer needed.

Prior versions only returned an error if the request failed to execute. For example if the client can't reach the server or the TLS handshake failed. With opensearch-go >= 3.0.0 each opensearchapi requests will return an error if the response http status code is > 299. The error can be parsed into the new `opensearchapi.Error` type by using `errors.As` to match for exceptions and get a more detailed view.

Before 3.0.0:

```go
// Create the request
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}

// Execute the requests
resp, err := createIndex.Do(ctx, client)
if err != nil {
	return err
}
// Close the body
defer resp.Body.Close()

// Check if the status code is >299
if createIndexResp.IsError() {
    fmt.Errorf("Opensearch returned an error. Status: %d", createIndexResp.StatusCode)
}
```

With 3.0.0:

```go
var opensearchError opensearchapi.Error
// Create and execute the requests
createResp, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
// Load err into opensearchapi.Error to access the fields and tolerate if the index already exists
if err != nil {
	if errors.As(err, &opensearchError) {
		if opensearchError.Err.Type != "resource_already_exists_exception" {
			return err
		}
	} else {
		return err
	}
}
```

### API Reorganization

Version 3.0.0 reorganized APIs into logical sub-clients. The following tables cover every method that moved, was renamed, or was removed.

**Naming conventions changed:**

- Request types: `*Request` to `*Req` (e.g., `SearchRequest` to `SearchReq`)
- Query parameters: separate `*Params` sub-struct (e.g., `SearchParams`)
- Functional options (`With*`) removed entirely
- `req.Do(ctx, client)` pattern removed; use `client.Method(ctx, req)` instead

#### Document Operations -- Moved to `client.Document`

| Before 3.0.0                          | With 3.0.0                                          |
| ------------------------------------- | --------------------------------------------------- |
| `client.Create(...)`                  | `client.Document.Create(ctx, req)`                  |
| `client.Delete(...)`                  | `client.Document.Delete(ctx, req)`                  |
| `client.DeleteByQuery(...)`           | `client.Document.DeleteByQuery(ctx, req)`           |
| `client.DeleteByQueryRethrottle(...)` | `client.Document.DeleteByQueryRethrottle(ctx, req)` |
| `client.Exists(...)`                  | `client.Document.Exists(ctx, req)`                  |
| `client.ExistsSource(...)`            | `client.Document.ExistsSource(ctx, req)`            |
| `client.Explain(...)`                 | `client.Document.Explain(ctx, req)`                 |
| `client.Get(...)`                     | `client.Document.Get(ctx, req)`                     |
| `client.GetSource(...)`               | `client.Document.Source(ctx, req)`                  |

#### Scroll Operations -- Moved to `client.Scroll`

| Before 3.0.0              | With 3.0.0                       |
| ------------------------- | -------------------------------- |
| `client.ClearScroll(...)` | `client.Scroll.Delete(ctx, req)` |
| `client.Scroll(...)`      | `client.Scroll.Get(ctx, req)`    |

#### Script Operations -- Moved to `client.Script`

| Before 3.0.0                         | With 3.0.0                                |
| ------------------------------------ | ----------------------------------------- |
| `client.DeleteScript(...)`           | `client.Script.Delete(ctx, req)`          |
| `client.GetScript(...)`              | `client.Script.Get(ctx, req)`             |
| `client.GetScriptContext(...)`       | `client.Script.Context(ctx, req)`         |
| `client.GetScriptLanguages(...)`     | `client.Script.Language(ctx, req)`        |
| `client.PutScript(...)`              | `client.Script.Put(ctx, req)`             |
| `client.ScriptsPainlessExecute(...)` | `client.Script.PainlessExecute(ctx, req)` |

#### Index Alias, Mapping, Settings -- Moved to Nested Sub-clients

| Before 3.0.0                          | With 3.0.0                               |
| ------------------------------------- | ---------------------------------------- |
| `client.Indices.DeleteAlias(...)`     | `client.Indices.Alias.Delete(ctx, req)`  |
| `client.Indices.ExistsAlias(...)`     | `client.Indices.Alias.Exists(ctx, req)`  |
| `client.Indices.GetAlias(...)`        | `client.Indices.Alias.Get(ctx, req)`     |
| `client.Indices.PutAlias(...)`        | `client.Indices.Alias.Put(ctx, req)`     |
| `client.Indices.UpdateAliases(...)`   | `client.Aliases(ctx, req)`               |
| `client.Indices.GetMapping(...)`      | `client.Indices.Mapping.Get(ctx, req)`   |
| `client.Indices.PutMapping(...)`      | `client.Indices.Mapping.Put(ctx, req)`   |
| `client.Indices.GetFieldMapping(...)` | `client.Indices.Mapping.Field(ctx, req)` |
| `client.Indices.GetSettings(...)`     | `client.Indices.Settings.Get(ctx, req)`  |
| `client.Indices.PutSettings(...)`     | `client.Indices.Settings.Put(ctx, req)`  |

#### Templates -- Moved to Top-level Sub-clients

| Before 3.0.0                                  | With 3.0.0                                     |
| --------------------------------------------- | ---------------------------------------------- |
| `client.Indices.DeleteIndexTemplate(...)`     | `client.IndexTemplate.Delete(ctx, req)`        |
| `client.Indices.ExistsIndexTemplate(...)`     | `client.IndexTemplate.Exists(ctx, req)`        |
| `client.Indices.GetIndexTemplate(...)`        | `client.IndexTemplate.Get(ctx, req)`           |
| `client.Indices.PutIndexTemplate(...)`        | `client.IndexTemplate.Create(ctx, req)`        |
| `client.Indices.SimulateIndexTemplate(...)`   | `client.IndexTemplate.SimulateIndex(ctx, req)` |
| `client.Indices.SimulateTemplate(...)`        | `client.IndexTemplate.Simulate(ctx, req)`      |
| `client.Indices.DeleteTemplate(...)`          | `client.Template.Delete(ctx, req)`             |
| `client.Indices.ExistsTemplate(...)`          | `client.Template.Exists(ctx, req)`             |
| `client.Indices.GetTemplate(...)`             | `client.Template.Get(ctx, req)`                |
| `client.Indices.PutTemplate(...)`             | `client.Template.Create(ctx, req)`             |
| `client.Cluster.DeleteComponentTemplate(...)` | `client.ComponentTemplate.Delete(ctx, req)`    |
| `client.Cluster.ExistsComponentTemplate(...)` | `client.ComponentTemplate.Exists(ctx, req)`    |
| `client.Cluster.GetComponentTemplate(...)`    | `client.ComponentTemplate.Get(ctx, req)`       |
| `client.Cluster.PutComponentTemplate(...)`    | `client.ComponentTemplate.Create(ctx, req)`    |

#### Data Streams -- Moved to `client.DataStream`

| Before 3.0.0                             | With 3.0.0                           |
| ---------------------------------------- | ------------------------------------ |
| `client.Indices.CreateDataStream(...)`   | `client.DataStream.Create(ctx, req)` |
| `client.Indices.DeleteDataStream(...)`   | `client.DataStream.Delete(ctx, req)` |
| `client.Indices.GetDataStream(...)`      | `client.DataStream.Get(ctx, req)`    |
| `client.Indices.GetDataStreamStats(...)` | `client.DataStream.Stats(ctx, req)`  |

#### Snapshot Repository -- Moved to `client.Snapshot.Repository`

| Before 3.0.0                             | With 3.0.0                                     |
| ---------------------------------------- | ---------------------------------------------- |
| `client.Snapshot.CreateRepository(...)`  | `client.Snapshot.Repository.Create(ctx, req)`  |
| `client.Snapshot.DeleteRepository(...)`  | `client.Snapshot.Repository.Delete(ctx, req)`  |
| `client.Snapshot.GetRepository(...)`     | `client.Snapshot.Repository.Get(ctx, req)`     |
| `client.Snapshot.CleanupRepository(...)` | `client.Snapshot.Repository.Cleanup(ctx, req)` |
| `client.Snapshot.VerifyRepository(...)`  | `client.Snapshot.Repository.Verify(ctx, req)`  |

#### Ingest -- Renamed Methods

| Before 3.0.0                        | With 3.0.0                       |
| ----------------------------------- | -------------------------------- |
| `client.Ingest.PutPipeline(...)`    | `client.Ingest.Create(ctx, req)` |
| `client.Ingest.DeletePipeline(...)` | `client.Ingest.Delete(ctx, req)` |
| `client.Ingest.GetPipeline(...)`    | `client.Ingest.Get(ctx, req)`    |
| `client.Ingest.ProcessorGrok(...)`  | `client.Ingest.Grok(ctx, req)`   |

#### Dangling Indices -- Moved to `client.Dangling`

| Before 3.0.0                                     | With 3.0.0                         |
| ------------------------------------------------ | ---------------------------------- |
| `client.DanglingIndicesDeleteDanglingIndex(...)` | `client.Dangling.Delete(ctx, req)` |
| `client.DanglingIndicesImportDanglingIndex(...)` | `client.Dangling.Import(ctx, req)` |
| `client.DanglingIndicesListDanglingIndices(...)` | `client.Dangling.Get(ctx, req)`    |

#### Other Renames

| Before 3.0.0                             | With 3.0.0                              |
| ---------------------------------------- | --------------------------------------- |
| `client.Count(...)`                      | `client.Indices.Count(ctx, req)`        |
| `client.FieldCaps(...)`                  | `client.Indices.FieldCaps(ctx, req)`    |
| `client.Mget(...)`                       | `client.MGet(ctx, req)`                 |
| `client.Msearch(...)`                    | `client.MSearch(ctx, req)`              |
| `client.MsearchTemplate(...)`            | `client.MSearchTemplate(ctx, req)`      |
| `client.Mtermvectors(...)`               | `client.MTermvectors(ctx, req)`         |
| `client.Indices.AddBlock(...)`           | `client.Indices.Block(ctx, req)`        |
| `client.Indices.ResolveIndex(...)`       | `client.Indices.Resolve(ctx, req)`      |
| `client.Cat.Fielddata(...)`              | `client.Cat.FieldData(ctx, req)`        |
| `client.Cat.Nodeattrs(...)`              | `client.Cat.NodeAttrs(ctx, req)`        |
| `client.Nodes.ReloadSecureSettings(...)` | `client.Nodes.ReloadSecurity(ctx, req)` |

#### Removed APIs (no v3+ equivalent)

- `client.TermsEnum(...)`
- `client.Cat.Help(...)`
- `client.Indices.DiskUsage(...)`
- `client.Indices.FieldUsageStats(...)`
- `client.Indices.GetUpgrade(...)`
- `client.Indices.Upgrade(...)`

## Upgrading to >= 2.3.0

### Snapshot Delete

`SnapshotDeleteRequest` and `SnapshotDelete` changed the argument `Snapshot` type from `string` to `[]string`.

Before 2.3.0:

```go
// If you have a string containing your snapshot
stringSnapshotsToDelete := "snapshot-1,snapshot-2"
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
	Snapshot: stringSnapshotsToDelete,
}

// If you have a slice of strings containing your snapshot
sliceSnapshotToDelete := []string{"snapshot-1","snapshot-2"}
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot: strings.Join(sliceSnapshotsToDelete, ","),
}
```

With 2.3.0:

```go
// If you have a string containing your snapshots
stringSnapshotsToDelete := strings.Split("snapshot-1,snapshot-2", ",")
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot:   stringSnapshotsToDelete,
}

// If you have a slice of strings containing your snapshots
sliceSnapshotToDelete := []string{"snapshot-1", "snapshot-2"}
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot: sliceSnapshotsToDelete,
}
```
