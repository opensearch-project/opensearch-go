# Upgrading to >= 4.x

## Upgrading to >= 4.7.0

### `opensearch.Request` interface signature change

`GetRequest()` now receives the HTTP method from the caller:

```go
// Before
GetRequest() (*http.Request, error)

// After
GetRequest(method string) (*http.Request, error)
```

This change is invisible to almost all callers: the typed `Req` structs that the client consumes (e.g. `opensearchapi.SearchReq`, `opensearchapi.IndexReq`) already implement the new signature. Only code that defines a custom type satisfying `opensearch.Request` is affected. If you maintain such a type, add a `method string` parameter and forward it to your underlying `http.NewRequest` call (or `opensearch.BuildRequest`).

### Path segment values are percent-encoded

Every typed `Req.GetRequest()` method now constructs URL paths through the generated `internal/path/*Path` builders, which unconditionally percent-encode user-supplied segment values (via `url.PathEscape`, plus an explicit `/` -> `%2F` substitution). This closes the [#650] path-injection class of bugs: an `Index` value containing `../../_cluster/health` can no longer escape its segment to alter routing.

Prior to this release, `buildPath` wrote segment values raw, which left the wire format ambiguous and the encoding contract undefined: callers who passed unencoded metacharacters got a malformed URL, and callers who passed pre-encoded values got the encoded bytes through to the server. Both interpretations existed simultaneously. The client now defines a single contract -- pass raw, unencoded values; the client encodes -- and applies it everywhere.

The practical consequence for callers who were already passing pre-encoded values is that those values are now double-encoded:

```go
// Before: the URL contained "my%2Findex" verbatim (relying on undefined behavior)
// After:  the URL contains "my%252Findex" (the percent itself is encoded)
client.Indices.Get(ctx, opensearchapi.IndicesGetReq{Index: []string{"my%2Findex"}})
```

If your code intentionally passes percent-encoded values, decode them with `url.PathUnescape` before populating the Req struct.

[#650]: https://github.com/opensearch-project/opensearch-go/issues/650

### `opensearchapi/` package - generated v5 API surface

The v5 `opensearchapi/` package is generated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification) by `cmd/osgen`, replacing the hand-written `opensearchapi/` package that shipped through v4. The same surface was distributed during the v4 branch as an early-access preview package for early adopters; it deliberately reused the package name `opensearchapi` so that callers who migrated during v4 only needed to change the import path at v5 release time -- every reference in code (e.g. `opensearchapi.IndexReq`, `opensearchapi.NewClient`) stays the same.

**Migration Considerations:**

- Migrating from the hand-written v4 `opensearchapi/` to the v5 generated surface is a single import-path edit per consuming file: change the module path from `/v4/opensearchapi` to `/v5/opensearchapi`. Package qualifiers do not change. (Early adopters of the v4 preview package likewise change their import to `/v5/opensearchapi`.)
- At v5, the hand-written `opensearchapi/` is removed; the only forward path is the code-generated API surface (closely matches the existing hand-written ergonomics).

**Import path:**

```go
import "github.com/opensearch-project/opensearch-go/v5/opensearchapi"

client, err := opensearchapi.NewClient(opensearchapi.Config{...})
```

**Surface differences worth knowing about:**

- Optional `Params` are `*Params` pointer fields (nil-safe; pass `&opensearchapi.IndexParams{...}` to set).
- Optional boolean query parameters are `*bool` so a deliberate `false` can be sent over the wire.
- Multi-index `Req` types use `Index []string` (the spec spelling); v4's hand-written `Indices` is renamed.
- Plugin APIs (k-NN, ML, Security, ISM, etc.) live in top-level `plugins/<name>` packages, imported as `github.com/opensearch-project/opensearch-go/v5/plugins/<name>`. v4's hand-written plugin clients (`opensearch-go/v4/plugins/{ism,security}`) are replaced by generated, spec-driven clients covering all 25 plugins; the package qualifier (e.g. `ism.X`) is unchanged.

For the full v4 -> v5 surface delta and the optional forward-compatible `replace` directive, see [`opensearchapi/UPGRADING_V4_TO_V5.md`](opensearchapi/UPGRADING_V4_TO_V5.md). For everyday usage (errors, routing, response handling) see [`opensearchapi/README.md`](opensearchapi/README.md).

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

The [`osapifix`](cmd/osapifix/README.md) tool automates the v3 -> v4 import bump and reports the error-model move (which it cannot rewrite mechanically) as a follow-up; see the deep-dive at [`opensearchapi/UPGRADING_V3_TO_V4.md`](opensearchapi/UPGRADING_V3_TO_V4.md).

### StringError for Unknown JSON Responses

Version 4.0.0 returns `*opensearch.StringError` error type instead of `*fmt.wrapError` when response received from the server is an unknown JSON. For example, consider delete document API which returns an unknown JSON body when document is not found.

Before 4.0.0:

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

After 4.0.0:

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
    // Fields are available directly -- no unmarshaling needed.
    fmt.Println(f.Index, f.Status, f.Cause)
}
```

### Inline `_shards` Structs Replaced with ResponseShards

The `Shards` field in `IndexResp`, `DocumentCreateResp`, `DocumentDeleteResp`, `UpdateResp`, `IndicesRefreshResp`, and `IndicesCountResp` changed from an anonymous inline struct to the named `ResponseShards` type.

**Who is affected:** Code accessing `resp.Shards.Total`, `resp.Shards.Successful`, or `resp.Shards.Failed` compiles unchanged. The only theoretical break is code using reflection or type assertions on the `Shards` field itself. The new type additionally exposes `Failures []ResponseShardsFailure` and `Skipped int` which were previously unavailable.

### `_type` Field Tags Now Include `omitempty`

All deprecated `_type` fields across response structs now use `json:"_type,omitempty"`. Mapping types were deprecated in Elasticsearch 6.0 (2017), reduced to the single `_doc` type in Elasticsearch 7.0 (2019), and completely removed from the OpenSearch server in 2.0.0 (May 2022). OpenSearch forked from Elasticsearch 7.10.2, so the deprecation was inherited from day one. This change means the empty string is no longer emitted when marshaling response structs back to JSON. Deserialization behavior is unchanged.
