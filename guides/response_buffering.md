# Response Body Lifecycle: `Do[T]` vs `Stream`

The OpenSearch Go client exposes two entry points for issuing requests, each with a different response-body ownership contract. Pick the one that matches your use case; do not mix them.

| Entry point                         | Body ownership | Buffering            | Use when                                                                 |
| ----------------------------------- | -------------- | -------------------- | ------------------------------------------------------------------------ |
| `opensearch.Do[T]`                  | SDK            | Buffered (in memory) | You want a typed, decoded Go value (CRUD, search, cluster ops). Default. |
| `opensearchtransport.Client.Stream` | Caller         | Unbuffered (raw)     | You want to forward or relay raw bytes downstream (proxy, streaming).    |

There is intentionally no typed streaming helper. "Stream and decode into `T`" is a contradiction: if you want `T`, use `Do[T]`; if you want bytes, use `Stream`.

## `Do[T]`: typed, buffered, default

`opensearch.Do[T]` (and the per-API `do(...)` helpers in `opensearchapi`, `v5preview/opensearchapi`, and `plugins/*`) call into `opensearchtransport.Client.Perform`, which:

1. Reads the entire response body into memory.
2. Closes the underlying body.
3. Replaces `Response.Body` with an `io.NopCloser` over a `bytes.Reader`.

This guarantees the underlying TCP connection is drained and returned to the connection pool even if the caller never reads the body, and lets the SDK decode the buffered bytes into a Go value.

```go
client, err := opensearchapi.NewClient(opensearchapi.Config{
    Client: opensearch.Config{
        Addresses: []string{"https://localhost:9200"},
        Username:  "admin",
        Password:  "myStrongPassword123!",
    },
})
if err != nil {
    return err
}

resp, err := client.Cluster.Health(ctx, &opensearchapi.ClusterHealthReq{})
if err != nil {
    return err
}
fmt.Println(resp.Status)
```

## `Stream`: raw, unbuffered, caller owns the body

`opensearchtransport.Client.Stream` returns the raw `*http.Response` from the underlying `http.RoundTripper`. The SDK does not read or close `res.Body`; the caller does. Stream still performs routing, retries, signing, header injection, request-body compression, and the seed URL fallback identically to `Perform`.

`opensearch.Client` exposes a `Stream` passthrough so callers do not need to type-assert `c.Transport`:

```go
res, err := client.Stream(req)
if err != nil {
    return err
}
defer res.Body.Close()
// io.Copy / decode incrementally / forward bytes downstream...
```

If the underlying transport is a custom implementation that does not satisfy the `opensearch.Streamer` interface, `client.Stream` returns `opensearch.ErrTransportMissingMethodStream`.

### Proxy and streaming example

A reverse proxy can use `io.Copy` (or `io.CopyBuffer` with a pooled buffer) to stream responses to downstream clients with minimal memory overhead:

```go
res, err := client.Stream(req)
if err != nil {
    http.Error(w, err.Error(), http.StatusBadGateway)
    return
}
defer res.Body.Close()

// Copy headers and status.
for k, v := range res.Header {
    w.Header()[k] = v
}
w.WriteHeader(res.StatusCode)

// Stream the body: bytes flow to the client as they arrive from OpenSearch.
if _, err := io.Copy(w, res.Body); err != nil {
    log.Printf("stream copy error: %v", err)
}
```

### Connection reuse with `Stream`

Because `Stream` does not buffer, the caller is responsible for the body lifecycle:

- **HTTP/2**: streams are multiplexed on a single connection, so draining is not required for connection reuse.
- **HTTP/1.1**: the caller MUST fully read the response body before the connection can be reused. If the caller abandons a partially-read body, the connection will be closed rather than returned to the pool.

In both cases, always call `res.Body.Close()` when done.

## When to use which

| Scenario                                           | Recommendation |
| -------------------------------------------------- | -------------- |
| Standard API calls (CRUD, search, cluster ops)     | `Do[T]`        |
| Reverse proxy forwarding large responses           | `Stream`       |
| Streaming bulk responses to clients                | `Stream`       |
| Scroll/PIT with large result sets piped downstream | `Stream`       |

## Deprecation note

`opensearchtransport.Client.Perform` and `opensearch.Client.Perform` are marked deprecated in v4 and will be removed in v5. They remain fully functional for v4 callers; the buffered-response contract is unchanged. New code should call `Do[T]` for typed results or `Stream` for raw byte forwarding.
