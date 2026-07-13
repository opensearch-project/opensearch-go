- [Compatibility with OpenSearch](#compatibility-with-opensearch)
- [Upgrading](#upgrading)

## Compatibility with OpenSearch

The below matrix shows the compatibility of the [`opensearch-go`](https://pkg.go.dev/github.com/opensearch-project/opensearch-go) with versions of [`OpenSearch`](https://opensearch.org/downloads.html#opensearch). Version ranges are inclusive of both endpoints.

| Client Version | OpenSearch Version |
| -------------- | ------------------ |
| 1.x.0          | 1.x                |
| 2.x.0          | 1.3.13 - 2.11.0    |
| 3.x.0          | 1.3.13 - 2.12.0    |
| 4.x.0          | 1.3.20 - 2.x       |
| 4.6.1          | 1.3.20 - 3.6.0     |
| 5.x.0          | 2.19 - 3.x         |

The 4.x client remains the supported path for OpenSearch lines that the OpenSearch project no longer patches (1.3.x through 2.18.x).

Starting with 5.x, the officially supported (CI-tested) set tracks the OpenSearch releases still receiving patches within the last 12 months at each `opensearch-go` release: every pinned release of the current major plus the latest release of the previous major. Today that is 2.19.x and 3.0.0 through 3.7.0. This set is re-evaluated at each release. Newer, untested releases are supported on a best-effort basis until they are added to the matrix. The client may still function against older servers, but those lines are not part of the tested matrix; 4.x remains the documented fallback for them.

## Upgrading

Major versions of OpenSearch introduce breaking changes that require careful upgrades of the client. While `opensearch-go-client` 2.0.0 works against the latest OpenSearch 1.x, certain deprecated features removed in OpenSearch 2.0 have also been removed from the client. Please refer to the [OpenSearch documentation](https://opensearch.org/docs/latest/clients/index/) for more information.
