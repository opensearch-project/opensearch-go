# Upgrading to >= 5.0.0

## Supported OpenSearch versions

The 5.x client is supported (and CI-tested) against the OpenSearch releases still receiving patches within the last 12 months at each `opensearch-go` release: every release of the current major plus the latest release of the previous major. Today that is OpenSearch 2.19.x and all of 3.x. This set is re-evaluated at each release.

If you run an OpenSearch line the project no longer patches (1.3.x through 2.18.x), stay on the 4.x client, which continues to support those versions. The 5.x client may still function against older servers, but those lines are not part of the tested matrix. See [`COMPATIBILITY.md`](COMPATIBILITY.md) for the full matrix.

## Partial Failure Errors (Config.Errors)

Version 5.0.0 introduces typed partial-failure errors and a per-category bitmask that controls which categories surface as Go errors. OpenSearch returns HTTP 200 for many operations that partially succeed (bulk item failures, shard failures on search, replica failures on writes). The new model turns those partial failures into typed errors that callers can dispatch on; idiomatic partial error handling is shown below.

**Default behavior change:**

| Surface | `Config.Errors == nil` means | Effect                                            |
| ------- | ---------------------------- | ------------------------------------------------- |
| v4      | `errmask.All`                | mask everything (preserves pre-bitfield behavior) |
| v5+     | `errmask.Empty`              | report every partial-failure category             |

A v4 caller upgrading to v5 who never set `Config.Errors` will start seeing partial failures as `error`. To preserve v4-style silence on v5, set `Errors: errmask.New(errmask.All)` explicitly. To opt v4 in to v5-style surfacing today, set `Errors: errmask.New()`.

**New surface (v5):**

- `Config.Errors *errmask.ErrorMask` field with the matrix above.
- `OPENSEARCH_GO_ERROR_MASK` environment-variable override (comma-separated `+`/`-` tokens of lowercase snake_case wrapper names like `bulk_items`, `search_shards`, `write_shards`, `multi_search_items`).
- Typed errors: `*PartialBulkError`, `*PartialSearchError`, `*ShardFailureError`, `*MultiSearchItemError`, `*MSearchErrors`, `*MSearchTemplateErrors`.
- `opensearchapi.Errors(err) []error` to flatten single- and multi-wrapper error shapes into a uniform slice.
- Helper functions: `IsPartialFailure(err)`, `ToleratePartialFailures(err)`, `RequireSuccessRate(err, threshold)`.
- Operation constants: `OperationIndex`, `OperationCreate`, `OperationUpdate`, `OperationDelete`.

The recommended call-site pattern is a `for`/`switch` over `opensearchapi.Errors(err)`, not `errors.As` against a specific type. Per-Resp helper methods (`BulkItemFailures()`, `SearchShardFailures()`, `WriteShardFailures()`, `MultiSearchItemFailures()`, `PartialFailures(mask)`) exist on the response types as engine machinery for the dispatch and remain available for focused inspection of a known category, but new code should use the type switch -- see [`guides/usage-error_handling.md`](guides/usage-error_handling.md#why-a-type-switch-not-errorsas-has-or-per-resp-helpers) for why.

**Where to read more:**

- [`opensearchapi/README.md`](opensearchapi/README.md) - full v5 usage guide for these errors, including the type-switch pattern and the rationale for preferring it over `errors.As`/`Has`.
- [`guides/usage-error_handling.md`](guides/usage-error_handling.md) - cross-version best-practices guide with v4 and v5 examples side-by-side.
- [`opensearchapi/UPGRADING_V4_TO_V5.md`](opensearchapi/UPGRADING_V4_TO_V5.md) - v4 -> v5 surface delta.
- [`cmd/osapifix/README.md`](cmd/osapifix/README.md) - the tool that automates most of the v4 -> v5 surface delta.

**Error types in v4 `opensearchapi/`** (the upgrade source):

| Error Type               | Returned By                                                                                                                         | Key Fields                                                                |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| `*PartialBulkError`      | `Bulk`                                                                                                                              | `FailedItems []BulkRespItem`, `SucceededCount int`                        |
| `*PartialSearchError`    | `Search`, `Scroll.Get`, `SearchTemplate` (single-bit); also via `*MSearchErrors` and `*MSearchTemplateErrors` for shard aggregation | `FailedShards int`, `TotalShards int`, `Failures []ResponseShardsFailure` |
| `*ShardFailureError`     | `Index`, `Document.Create`, `Document.Delete`, `Update`                                                                             | `Operation string`, `FailedShards int`, `TotalShards int`                 |
| `*MultiSearchItemError`  | `MSearch`, `MSearchTemplate` (per-sub-response error inspection)                                                                    | `Items []MultiSearchItemFailure`, `SucceededCount int`                    |
| `*MSearchErrors`         | `MSearch` when 2+ wrappers fire                                                                                                     | `Unwrap() []error` (multi-error contract)                                 |
| `*MSearchTemplateErrors` | `MSearchTemplate` when 2+ wrappers fire                                                                                             | `Unwrap() []error`                                                        |

The v5 surface ports the same model with internal field types regenerated from the [OpenSearch API specification](https://github.com/opensearch-project/opensearch-api-specification) ([see UPGRADING_V4_TO_V5.md](opensearchapi/UPGRADING_V4_TO_V5.md#partial-failure-type-renames) for the table).

## Default Router Injection in v5

`opensearchapi.NewClient` (and `NewDefaultClient`) now inject [`opensearchtransport.NewDefaultRouter`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#NewDefaultRouter) when the caller leaves `config.Client.Router` nil. The `OPENSEARCH_GO_ROUTER` environment variable acts as an opt-out:

| `OPENSEARCH_GO_ROUTER` | v4                                                  | v5                                                          |
| ---------------------- | --------------------------------------------------- | ----------------------------------------------------------- |
| unset                  | no Router, no auto-discovery                        | **default Router injected, auto-discovery on**              |
| `true` / `1`           | default Router (transport layer), auto-discovery on | **default Router injected, auto-discovery on**              |
| `false` / `0`          | no Router, no auto-discovery                        | **injection skipped (Router stays nil)**, no auto-discovery |
| unparseable            | no Router, no auto-discovery                        | default Router injected, auto-discovery on                  |

In v5 the Router and on-start discovery are on unless `OPENSEARCH_GO_ROUTER` is explicitly false (`false` / `0`); only that explicit opt-out disables them. A caller-supplied `DiscoverNodesOnStart` value always wins over the env-var-driven side-effect.

v4's `opensearchapi.NewClient` did not auto-inject a Router, so v4 code keeps its original behavior; v5 flips the default so the Router is on unless `OPENSEARCH_GO_ROUTER=false`.

For full usage and rationale see [`guides/config-envvars.md` Default router injection](guides/config-envvars.md#default-router-injection).

## `DiscoverNodes()` blocking semantics

`opensearch.Client.DiscoverNodes()` and `opensearchtransport.Transport.DiscoverNodes()` now **block** when an in-flight discovery cycle is active and return that cycle's error verbatim, instead of the previous immediate `nil` no-op.

The previous "fire-and-forget" behavior masked discovery failures: a caller that handed control back after a failing discovery saw `err == nil` and continued against a stale node list. The new behavior surfaces the failure synchronously so callers can react (retry, alert, fall back to seed nodes).

Client construction itself never blocks on discovery: when `Config.DiscoverNodesOnStart` is `&true`, `opensearch.NewClient` launches a detached goroutine that calls `DiscoverNodes()`, then returns. Callers who want fully manual control set `DiscoverNodesOnStart: &false` and `DiscoverNodesInterval: 0` -- no on-start goroutine, no polling loop -- and call `client.DiscoverNodes(ctx)` themselves before issuing requests:

```go
discoverOnStart := false
client, err := opensearch.NewClient(opensearch.Config{
    Addresses:             []string{"https://localhost:9200"},
    DiscoverNodesOnStart:  &discoverOnStart, // skip auto-discovery
    DiscoverNodesInterval: 0,                // disable polling loop
})
if err != nil {
    log.Fatal(err)
}

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
if err := client.DiscoverNodes(ctx); err != nil {
    log.Printf("initial discovery failed: %s", err)
}

// proceed to use the client; topology data is ready.
```

The signature already changed earlier in this version to take `context.Context` (see CHANGELOG); this is a behavioral change on top of the signature change.

## Metrics error on disabled removed

The per-request transport counters (`Requests`, `Failures`, and responses-by-status) are now always collected via lock-free atomics, independent of `EnableMetrics`. As a result, `opensearch.Client.Metrics()` (and `opensearchtransport.Transport.Metrics()`) no longer returns the `"transport metrics not enabled"` error when `EnableMetrics` is false -- it always returns the per-request counters. `EnableMetrics` now gates only the detailed-metrics snapshot (per-connection enumeration, per-policy breakdowns, and router cache state); those fields stay zero/nil when it is unset.

Callers that branched on the error to detect the disabled state should drop that check. The returned error is now non-nil only when a detailed-metrics snapshot callback fails.

```go
// v4: Metrics() errored when EnableMetrics was false, so callers used the
// error to detect the disabled state.
m, err := client.Metrics()
if err != nil {
    // treated as "metrics disabled" -- no counters available
    return
}
use(m.Requests, m.Failures)

// v5: per-request counters are always populated. A non-nil error now means a
// detailed-snapshot callback failed, not that metrics are disabled.
m, err := client.Metrics()
if err != nil {
    log.Printf("detailed metrics snapshot failed: %s", err)
    // m.Requests / m.Failures / m.Responses are still valid here
}
use(m.Requests, m.Failures)
```

Detailed fields such as `Policies` and `Router` remain populated only when `EnableMetrics` is set; reading them without it yields nil, unchanged from v4.

> Note: a later v5 change removed `EnableMetrics` entirely -- see [`EnableMetrics` removed](#enablemetrics-removed) below. The behavior described above was the intermediate state; in the shipped v5 the detailed fields are always populated.

## `EnableMetrics` removed

`EnableMetrics` has been removed from both `opensearch.Config` and `opensearchtransport.Config`. The detailed-metrics snapshot (per-connection enumeration, per-policy breakdowns, and router cache state) is now always available -- it is assembled lazily and lock-free at the moment you call `Metrics()`, so it adds no per-request cost. The per-request counters were already always-on.

Delete any `EnableMetrics` field from your config; leaving it in place is a compile error.

`Metrics()` now returns the full snapshot unconditionally, including `Connections`, `Policies`, and `Router` (the latter two populate when a router with policies is active). The returned error is still non-nil only when a snapshot callback fails.

## `opensearchtransport.Client` renamed to `opensearchtransport.Transport`

The concrete `opensearchtransport.Client` type was renamed to `opensearchtransport.Transport`. The type owns HTTP round-trip concerns -- connection pooling, retries, node selection, and discovery -- so `Transport` reflects its role and avoids colliding conceptually with the API clients above it (`opensearch.Client` and `opensearchapi.Client`).

The `Client` name is removed entirely; there is no compatibility alias. Update any references to the concrete type:

```go
// Before
var t *opensearchtransport.Client

// After
var t *opensearchtransport.Transport
```

The `opensearchtransport.New` constructor is unchanged -- it already returns `*Transport`, so callers that only use `New(...)` need no changes. `opensearch.Client` and `opensearchapi.Client` are unaffected by this rename.

## `opensearchtransport.Route` interface gained `OpID()`

The exported `Route` interface in `opensearchtransport` gained a new method:

```go
type Route interface {
    Policy() Policy
    Attrs() routeAttr
    PoolName() string
    OpID() OperationID  // new in v5
}
```

External code that implements `Route` (custom routing policies) must add an `OpID() OperationID` method returning the [`OperationID`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#OperationID) for the route -- typically the `Op*` constant matching the route's HTTP method+path. Built-in routes built via `NewRouteMux` are populated automatically; only hand-written `Route` implementations are affected.

## `Perform` removed; `Stream` is now the transport interface method

`opensearchtransport.Interface` previously required `Perform(*http.Request) (*http.Response, error)`, which buffered the entire response body before returning. It now requires two methods:

- `Stream(*http.Request) (*http.Response, error)` -- returns the raw, unbuffered body; the caller owns and must close it.
- `Request(*http.Request) (*http.Response, error)` -- buffers the body (draining the connection back to the pool) and returns it as an `io.NopCloser` over the buffered bytes, the same contract the old `Perform` had.

**Custom transport implementations** must provide both methods. The old `Perform` maps directly onto `Request`:

```go
// Before
func (t *MyTransport) Perform(req *http.Request) (*http.Response, error) {
    resp, err := t.inner.RoundTrip(req)
    // ... any buffering logic ...
    return resp, err
}

// After: Stream returns the raw body ...
func (t *MyTransport) Stream(req *http.Request) (*http.Response, error) {
    return t.inner.RoundTrip(req)
}

// ... and Request buffers it (old Perform behavior).
func (t *MyTransport) Request(req *http.Request) (*http.Response, error) {
    resp, err := t.inner.RoundTrip(req)
    // ... buffer resp.Body into an io.NopCloser over a bytes.Reader ...
    return resp, err
}
```

**Callers of `(*opensearch.Client).Perform`** should switch to the appropriate alternative:

- Use `client.Stream(req)` for raw byte forwarding (incremental/streaming use cases). You are responsible for closing `resp.Body`.
- Use `opensearch.Execute[T](ctx, client, method, req, &result)` for typed, decoded responses.

The `opensearch.Streamer` interface and `opensearch.ErrTransportMissingMethodStream` sentinel are removed; `Stream` is now guaranteed on every `opensearchtransport.Interface` implementation.

## `Response.RawBody()` for buffered response bytes

`Response.Body` remains a public `io.ReadCloser` field; reading it is unchanged:

```go
body, err := io.ReadAll(resp.Body)
```

For responses decoded by `opensearch.Execute`, the buffered bytes are also available without consuming the body reader via the `RawBody() []byte` method (useful for inspection or comparison testing):

```go
raw := resp.RawBody() // nil for streamed responses (Client.Stream); read resp.Body directly there
```

## `signer/aws` removed in favor of `signer/awsv2`

The `signer/aws` package is removed. Use `signer/awsv2`, whose name mirrors AWS's own SDK-version nomenclature.

For callers coming from released v4, this is a full AWS SDK v1 -> v2 signer migration, not just an import swap: v4's `signer/aws` was built on AWS SDK for Go v1, while `signer/awsv2` is SDK v2.

```go
// Before (v4 signer/aws, AWS SDK v1)
import requestsigner "github.com/opensearch-project/opensearch-go/v4/signer/aws"

opts := session.Options{ /* ... */ }
signer, err := requestsigner.NewSignerWithService(opts, requestsigner.OpenSearchServerless)

// After (v5 signer/awsv2, AWS SDK v2)
import requestsigner "github.com/opensearch-project/opensearch-go/v5/signer/awsv2"

cfg, err := config.LoadDefaultConfig(context.TODO())
// ...
signer, err := requestsigner.NewSignerWithService(cfg, "aoss")
```

What changes:

- **Constructor input**: `session.Options` becomes `aws.Config` (built with `config.LoadDefaultConfig`).
- **Return type**: `*signer/aws.Signer` becomes the `signer.Signer` interface.
- **Service constants**: `signer/aws` exported `OpenSearchService` (`"es"`) and `OpenSearchServerless` (`"aoss"`); `signer/awsv2` does not, so pass the `"es"` / `"aoss"` literal directly.
- **Optional `SignerOptions`**: `signer/awsv2` additionally accepts functional `SignerOptions` to customize the underlying SigV4 signer.

See [USER_GUIDE.md](USER_GUIDE.md#amazon-opensearch-service) for a full example.

## Client-side DNS caching on by default

When no custom `Transport` is supplied, v5 installs a process-local DNS cache on the client's HTTP transport. Resolved addresses are cached and re-resolved on an interval (default 60s), and when the resolver is briefly unreachable the last-known-good address keeps being served until it recovers. v4 performed a fresh lookup per dial via the stock `http.Transport`.

This changes runtime networking for every existing user. The serve-stale behavior means a host whose IP changes -- failover, blue-green, or a scale event -- can keep receiving the previous address for up to the refresh interval before the cache re-resolves. For most deployments this is a resilience win (a transient DNS blip no longer fails requests to already-resolved hosts), but if your topology relies on immediate DNS cutover you can tune or disable it:

```go
// Tune the re-resolution interval (programmatic).
cfg := opensearch.Config{
    DNSCacheRefresh: 10 * time.Second,
}

// Disable caching entirely, restoring v4 per-dial resolution.
cfg := opensearch.Config{
    DNSCacheRefresh: -1, // <0 disables; 0 uses the 60s default
}
```

The same knobs are available via environment variables (`OPENSEARCH_GO_DNS_CACHE_REFRESH=-1` to disable), and `DNSDialTimeout` / `DNSKeepAlive` tune the underlying dialer. A caller-supplied `Transport` is never modified, so any client that sets its own `Transport` is unaffected and opts out implicitly. Because Go's resolver does not expose record TTLs, the interval is a re-resolution cadence rather than a per-record TTL.

## Default client caching

v5 caches implicitly-created default clients. `opensearch.NewDefaultClient`, `opensearchapi.NewDefaultClient`, and the client `opensearchutil.NewBulkIndexer` builds when none is supplied now resolve identical configs to one shared, refcounted transport keyed by config hash, instead of constructing an independent transport (and its goroutines and connection pool) per call. v4 built a fresh transport every time.

The observable change is `Metrics()`. Because two default clients with identical config share one transport, their per-request counters are aggregated across every holder rather than isolated per client:

```go
a, _ := opensearch.NewDefaultClient()
b, _ := opensearch.NewDefaultClient() // same config -> same shared transport as a

// a and b now report the combined request/failure counts for both, not each client's own.
```

If you built multiple default clients specifically to read separate metrics, switch to `opensearch.NewClient`/`opensearchapi.NewClient`, which are never cached and always get their own transport:

```go
a, _ := opensearch.NewClient(opensearch.Config{}) // independent transport, isolated metrics
b, _ := opensearch.NewClient(opensearch.Config{}) // independent transport, isolated metrics
```

To turn caching off process-wide, set `OPENSEARCH_GO_DEFAULT_CLIENT_TTL` to a negative value (e.g. `-1` or `-1s`) so every call builds a fresh client. The variable otherwise tunes the idle eviction window and accepts either a `time.ParseDuration` string (`16m`) or a bare number of seconds (`30`, `1.5`); default `16m`, `0` never evicts. Call `Close()` on a default client when done so its shared transport can be reclaimed once no holder remains and it goes idle.
