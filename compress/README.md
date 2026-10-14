# compress

`compress` provides request-body compressors for the OpenSearch Go client, backed by [klauspost/compress](https://github.com/klauspost/compress).

The client compresses request bodies through the `opensearchtransport.Compressor` interface, which `opensearch.Config.Compressor` accepts. The root module ships only the standard library's gzip (`opensearchtransport.GZip`). This module adds gzip, deflate and snappy encoders from klauspost/compress.

`compress` is a separate Go module, so klauspost/compress stays out of the core client's dependency graph. You opt in by importing it. No klauspost type appears in its API.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/compress/v5
```

## Codecs

| Constructor      | Content-Encoding | Levels                 |
| ---------------- | ---------------- | ---------------------- |
| `Gzip(level)`    | `gzip`           | `DefaultLevel`, 0 to 9 |
| `Deflate(level)` | `deflate`        | `DefaultLevel`, 0 to 9 |
| `Snappy()`       | `snappy`         | none                   |

`DefaultLevel` is -1 and selects klauspost's default, level 5. The standard library's `gzip.DefaultCompression`, the usual level for `opensearchtransport.GZip`, is level 6.

`Deflate` writes the zlib format, which is what the `deflate` Content-Encoding denotes. `Snappy` writes the framed format. A constructor that takes a level returns an error for a level outside the range in the table, so a bad level fails when you build the client, not on the first request.

## Usage

```go
c, err := compress.Gzip(compress.DefaultLevel)
if err != nil {
	return err
}

client, err := opensearch.NewClient(opensearch.Config{
	Addresses:  []string{"https://localhost:9200"},
	Compressor: c,
})
```

To compress only bulk traffic, build a separate client with the `Compressor` set and pass it as `opensearchutil.BulkIndexerConfig.Client`. Other requests keep using the client without one. See `Example_bulkIndexer`.

## Server support

The server has to decode the Content-Encoding a request carries. This module's integration test (`compress_integration_test.go`, run by `make test-integ` against a live cluster) sends a bulk request with each codec and counts the documents.

## Performance

`BenchmarkEncodeNDJSON` encodes about 1 MiB of bulk-shaped NDJSON per codec on a reused encoder, and reports throughput, allocations and the compression ratio. The `gzip-stdlib` row runs at the standard library default (level 6) and `gzip-klauspost` at level 5, so the two rows are not at equal levels. Reproduce with `go test -run=none -bench=. -benchmem ./...`.
