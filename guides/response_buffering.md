# Response Body Buffering

By default, the OpenSearch Go client reads and buffers the entire HTTP response body in `Perform()` before returning it to the caller. This guarantees that the underlying TCP connection is drained and returned to the connection pool, even if the caller does not fully read the body.

For most use cases this is the right behavior. However, for **proxy** and **streaming** workloads where the caller forwards large responses incrementally, buffering adds memory pressure and increases time-to-first-byte (TTFB) because the entire body must be read before the caller sees any bytes.

## Disabling Response Buffering

Set `DisableResponseBuffering: true` to skip the buffering step. Perform() returns the raw `http.Response.Body` from the underlying `RoundTrip`, and the **caller is responsible for fully reading and closing it**.

```go
client, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},
    Username:  "admin",
    Password:  "myStrongPassword123!",

    // Skip response body buffering — the caller will stream the body.
    DisableResponseBuffering: true,
})
```

### Proxy example

A reverse proxy can use `io.Copy` (or `io.CopyBuffer` with a pooled buffer) to stream responses to downstream clients with minimal memory overhead:

```go
res, err := client.Perform(req)
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

// Stream the body — bytes flow to the client as they arrive from OpenSearch.
if _, err := io.Copy(w, res.Body); err != nil {
    log.Printf("stream copy error: %v", err)
}
```

## Connection Reuse

The buffering exists to ensure HTTP/1.1 connections are properly drained and returned to the pool. When buffering is disabled:

- **HTTP/2**: Streams are multiplexed on a single connection, so draining is not required for connection reuse. This is the recommended protocol when disabling buffering.
- **HTTP/1.1**: The caller **must** fully read the response body before the connection can be reused. If the caller abandons a partially-read body, the connection will be closed rather than returned to the pool.

In both cases, always call `res.Body.Close()` when done.

## When to Use

| Scenario | Recommendation |
| --- | --- |
| Standard API calls (CRUD, search, cluster ops) | Leave buffering enabled (default) |
| Reverse proxy forwarding large responses | Disable buffering |
| Streaming bulk responses to clients | Disable buffering |
| Scroll/PIT with large result sets piped downstream | Disable buffering |

## Configuration Reference

| Field | Type | Default | Location |
| --- | --- | --- | --- |
| `DisableResponseBuffering` | `bool` | `false` | `opensearch.Config`, `opensearchtransport.Config` |
