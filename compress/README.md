# compress

`compress` provides request-body compressors for the OpenSearch Go client, backed by [klauspost/compress](https://github.com/klauspost/compress).

The client compresses request bodies through the `opensearchtransport.Compressor` interface, which `opensearch.Config.Compressor` accepts, or through an `opensearchtransport.CompressorTable`, which `opensearch.Config.CompressorTable` accepts and which chooses the Compressor by server version. The root module ships only the standard library's gzip (`opensearchtransport.GZip`). This module adds gzip, deflate, snappy and zstd encoders from klauspost/compress.

`compress` is a separate Go module, so klauspost/compress stays out of the core client's dependency graph. You opt in by importing it. No klauspost type appears in its API.

## Install

```sh
go get github.com/opensearch-project/opensearch-go/compress/v5
```

## Codecs

| Constructor        | Content-Encoding | Levels                  | Servers                     | Set as                              |
| ------------------ | ---------------- | ----------------------- | --------------------------- | ----------------------------------- |
| `Gzip(level)`      | `gzip`           | `DefaultLevel`, 0 to 9  | all                         | `opensearch.Config.Compressor`      |
| `Deflate(level)`   | `deflate`        | `DefaultLevel`, 0 to 9  | all                         | `opensearch.Config.Compressor`      |
| `Snappy()`         | `snappy`         | none                    | all                         | `opensearch.Config.Compressor`      |
| `ZstdTable(level)` | `zstd`           | `DefaultLevel`, 1 to 22 | 3.0.0 and later; gzip below | `opensearch.Config.CompressorTable` |

`DefaultLevel` is -1 and selects klauspost's default, level 5. The standard library's `gzip.DefaultCompression`, the usual level for `opensearchtransport.GZip`, is level 6. For `ZstdTable`, klauspost groups the levels: 1 to 2 fastest, 3 to 5 default (what `DefaultLevel` selects), 6 to 9 better compression, 10 to 22 best compression; see [`EncoderLevelFromZstd`](https://pkg.go.dev/github.com/klauspost/compress/zstd#EncoderLevelFromZstd). `ZstdTable` encodes with a 1 MiB window and klauspost's lower-memory mode, so each encoder holds about 1 MiB of history rather than 8 to 16 MiB; bulk bodies compress as well at that window.

`Deflate` writes the zlib format, which is what the `deflate` Content-Encoding denotes. `Snappy` writes the framed format. A constructor that takes a level returns an error for a level outside the range in the table, so a bad level fails when you build the client, not on the first request.

### zstd and server versions

zstd is supported from OpenSearch 3.0.0. With node discovery, `ZstdTable` sends zstd to each node once it runs 3.0.0 or later, and gzip at level 6 to older or unknown versions. Through a seed URL or an address rewritten by an `AddressResolverFunc`, which may front several nodes, it sends zstd only while every node in the cluster runs 3.0.0 or later.

Through a seed URL or rewritten address, the client reads node versions with `GET /_nodes`, which needs the `cluster:monitor/nodes/info` permission; without it, that traffic stays on gzip. See the [`CompressorTable` godoc](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchtransport#CompressorTable) for how versions are read.

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

To send zstd to servers that support it, set a `ZstdTable` as `CompressorTable`:

```go
table, err := compress.ZstdTable(compress.DefaultLevel)
if err != nil {
	return err
}

client, err := opensearch.NewClient(opensearch.Config{
	Addresses:       []string{"https://localhost:9200"},
	CompressorTable: table,
})
```

`Compressor` and `CompressorTable` cannot both be set.

Bodies shorter than `opensearch.Config.CompressMinBytes`, 4096 bytes by default, are sent uncompressed, because compressing one saves less time than it costs on most links.

To compress only bulk traffic, build a separate client with the `Compressor` set and pass it as `opensearchutil.BulkIndexerConfig.Client`. Other requests keep using the client without one. See `Example_bulkIndexer`.

## Server support

The server has to decode the Content-Encoding a request carries. This module's integration test (`compress_integration_test.go`, run by `make test-integ` against a live cluster) sends a bulk request with each codec and counts the documents. On servers older than 3.0.0 it checks that `ZstdTable` sends gzip.

## Performance

`BenchmarkEncodeNDJSON` encodes about 1 MiB of bulk-shaped NDJSON per codec on a reused encoder, and reports throughput, allocations and the compression ratio. The `gzip-stdlib` row runs at the standard library default (level 6) and `gzip-klauspost` at level 5, so the two rows are not at equal levels. Reproduce with `go test -run=none -bench=. -benchmem ./...`.

### Minimum body size

`CompressMinBytes` defaults to 4096 because small bodies save too little to pay for compressing them. The table gives the link speed below which compressing a body of each size makes the request finish sooner, counting the client's compression time and the server's decompression CPU. Ranges span an access-log corpus, a corpus of nested JSON documents and generated JSON with random schemas, all from the OpenSearch Benchmark workloads except the last.

| Body size | gzip level 6   | zstd level 1   |
| --------- | -------------- | -------------- |
| 1 KiB     | 0.11-0.17 Gbps | 0.12-0.19 Gbps |
| 2 KiB     | 0.25-0.44 Gbps | 0.30-0.55 Gbps |
| 4 KiB     | 0.43-1.2 Gbps  | 0.56-1.2 Gbps  |
| 16 KiB    | 0.69-1.6 Gbps  | 1.05-2.3 Gbps  |
| 1 MiB     | 0.44-1.5 Gbps  | 1.04-3.8 Gbps  |

A 2 KiB body saves about 1.5 KB, which pays only on links slower than about 0.55 Gbps. Measured with one client core on an Apple M4 Max against a single-node OpenSearch 3.9.0 on 4 vCPUs, over loopback. On a slow or metered link, a lower `CompressMinBytes` such as 1024 still pays; a negative value compresses every body.
