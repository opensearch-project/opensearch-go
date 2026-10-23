# internal/third_party/jsonv2

A copy of the Go standard library's `encoding/json/v2`, taken from the toolchain named in [`VERSION`](VERSION), under the Go Authors' BSD 3-Clause license in [`LICENSE`](LICENSE).

## Why it is here

`internal/jsonx` runs the client's JSON on the `encoding/json/v2` engine whatever the toolchain. On Go 1.27, which builds `encoding/json/v2` by default, `encoding/json` already runs on it, and `jsonx` uses the standard library. The module's `go` directive is Go 1.26, whose `encoding/json` runs the original engine, which makes about 2.7 times the allocations decoding an mget response. On Go 1.26, with or without `GOEXPERIMENT=jsonv2`, `jsonx` runs this copy instead (`internal/jsonx/codec_copy.go`).

## What it holds

| Directory                                                                  | Copied from                                                                                                  |
| -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| `json`                                                                     | `encoding/json/v2`                                                                                           |
| `jsontext`                                                                 | `encoding/json/jsontext`                                                                                     |
| `internal`, `internal/jsonflags`, `internal/jsonopts`, `internal/jsonwire` | `encoding/json/internal`, and the same subdirectories                                                        |
| `v1`                                                                       | `encoding/json`'s `v2_*.go`, which run `encoding/json`'s API on the v2 engine, with the `v2_` prefix dropped |

The files are the standard library's, with two changes and no others: each file's `//go:build goexperiment.jsonv2` line is removed, and the `encoding/json/v2`, `encoding/json/jsontext` and `encoding/json/internal` import paths name this directory. Tests are not copied, since several import `internal/testenv`, which only the standard library can import; the code is tested as the standard library's, and `internal/jsonx` checks it against `encoding/json`.

## Updating it

Run [`copy.sh`](copy.sh) with the toolchain to copy from, then run the unit tests on both engines:

```sh
GOTOOLCHAIN=go1.27.3 internal/third_party/jsonv2/copy.sh
go test ./...
GOTOOLCHAIN=go1.27.3 go test ./...
```

## Deleting it

Once the module's `go` directive is Go 1.27 or later, delete this directory and `internal/jsonx/codec_copy.go`, remove the build constraint from `internal/jsonx/codec_std.go`, the exclusion in `.github/check-license-headers.sh`, and the entry in `NOTICE.txt`.
