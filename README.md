[![Go Reference](https://pkg.go.dev/badge/github.com/opensearch-project/opensearch-go.svg)](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5) [![Build](https://github.com/opensearch-project/opensearch-go/actions/workflows/lint.yml/badge.svg)](https://github.com/opensearch-project/opensearch-go/actions/workflows/lint.yml) [![Unit](https://github.com/opensearch-project/opensearch-go/actions/workflows/test-unit.yml/badge.svg)](https://github.com/opensearch-project/opensearch-go/actions/workflows/test-unit.yml) [![Integration](https://github.com/opensearch-project/opensearch-go/actions/workflows/test-integration.yml/badge.svg)](https://github.com/opensearch-project/opensearch-go/actions/workflows/test-integration.yml) [![codecov](https://codecov.io/gh/opensearch-project/opensearch-go/branch/main/graph/badge.svg?token=MI9g3KYHVx)](https://app.codecov.io/gh/opensearch-project/opensearch-go) [![Chat](https://img.shields.io/badge/chat-on%20forums-blue)](https://forum.opensearch.org/c/clients/60) ![PRs welcome!](https://img.shields.io/badge/PRs-welcome!-success)

![OpenSearch logo](OpenSearch.svg)

OpenSearch Go Client

- [Welcome!](#welcome)
- [Quickstart](#quickstart)
- [Project Resources](#project-resources)
- [Code of Conduct](#code-of-conduct)
- [License](#license)
- [Copyright](#copyright)

## Welcome!

**opensearch-go** is [a community-driven, open source fork](https://aws.amazon.com/blogs/opensource/introducing-opensearch/) of go-elasticsearch licensed under the [Apache v2.0 License](LICENSE.txt). For more information, see [opensearch.org](https://opensearch.org/).

The client supports automatic node discovery, request-based connection routing, and role-aware node selection. See the [User Guide](USER_GUIDE.md) and [guides](guides/) for usage examples and configuration options.

Upgrading across a major version? The [`osapilint`](cmd/osapilint/README.md) tool automates most of the API-shape changes (type, method, and field renames) - see [UPGRADING_V4_TO_V5.md](opensearchapi/UPGRADING_V4_TO_V5.md).

## Quickstart

Install the client:

```shell
go get github.com/opensearch-project/opensearch-go/v5
```

A small CRUD example - create an index, index a document, read it back, search for it, then delete it:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func main() {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{"https://localhost:9200"},
			Username:  "admin",
			Password:  "myStrongPassword123!",
		},
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()

	// Create an index.
	if _, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index:      "movies",
		BodyReader: strings.NewReader(`{"settings": {"number_of_shards": 1}}`),
	}); err != nil {
		log.Fatal(err)
	}

	// Index two documents. Omitting ID lets OpenSearch generate one, returned as
	// resp.ID. This keeps the example simple, but in production prefer a
	// client-supplied ID derived from your data's natural key: it makes indexing
	// idempotent (a retry overwrites rather than duplicates) and lets you address
	// the document later without storing the server's ID. Refresh=true makes the
	// documents immediately searchable, which is convenient here but hurts
	// indexing throughput at scale - prefer the default refresh in production.
	movies := []string{
		`{"title": "WarGames", "year": 1983}`,
		`{"title": "Sneakers", "year": 1992}`,
	}
	var ids []string
	for _, doc := range movies {
		indexed, err := client.Doc.Index(ctx, opensearchapi.IndexReq{
			Index:  "movies",
			Body:   strings.NewReader(doc),
			Params: &opensearchapi.IndexParams{Refresh: "true"},
		})
		if err != nil {
			log.Fatal(err)
		}
		ids = append(ids, indexed.ID)
	}

	// Get the first document back by its generated ID.
	got, err := client.Doc.Get(ctx, opensearchapi.GetReq{Index: "movies", ID: ids[0]})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("get id=%s found=%v source=%q\n", got.ID, got.Found, got.Source)

	// Search for WarGames; with two documents indexed, this confirms we get the
	// right one back rather than just "a" result.
	search, err := client.Search(ctx, &opensearchapi.SearchReq{
		Indices:    []string{"movies"},
		BodyReader: strings.NewReader(`{"query": {"match": {"title": "WarGames"}}}`),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d hit(s)\n", len(search.Hits.Hits))
	for _, hit := range search.Hits.Hits {
		fmt.Printf("  source=%q\n", hit.Source)
	}

	// Delete both documents.
	for _, id := range ids {
		if _, err = client.Doc.Delete(ctx, opensearchapi.DeleteReq{Index: "movies", ID: id}); err != nil {
			log.Fatal(err)
		}
	}
}
```

Next steps:

- [Security](guides/config-security.md) - TLS, certificate verification, and authentication. The example above uses a plaintext-friendly local setup; read this before connecting to a real cluster.
- [Environment Variables](guides/config-envvars.md) - the canonical reference for every `OPENSEARCH_GO_*` runtime override.
- [`opensearchapi` usage README](opensearchapi/README.md) - client creation, requests, responses, query parameters, and partial-failure errors.
- [Guides](guides/) - task-oriented references (indexing, search, bulk, routing, discovery, error handling, and more).
- [API reference on pkg.go.dev](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi) - the full generated API surface.

## Project Resources

- [Project Website](https://opensearch.org/)
- [Developer Guide](DEVELOPER_GUIDE.md)
- [User Guide](USER_GUIDE.md)
- [Upgrade Tool (`osapilint`)](cmd/osapilint/README.md)
- [Documentation](https://docs.opensearch.org/latest/clients/go/)
- [API Documentation](https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5)
- Need help? Try [Forums](https://forum.opensearch.org/c/clients/60)
- [Project Principles](https://opensearch.org/#principles)
- [Contributing to OpenSearch](CONTRIBUTING.md)
- [Maintainer Responsibilities](MAINTAINERS.md)
- [Release Management](RELEASING.md)
- [Admin Responsibilities](ADMINS.md)
- [Security](SECURITY.md)

## Code of Conduct

This project has adopted the [Amazon Open Source Code of Conduct](CODE_OF_CONDUCT.md). For more information see the [Code of Conduct FAQ](https://aws.github.io/code-of-conduct-faq), or contact [opensource-codeofconduct@amazon.com](mailto:opensource-codeofconduct@amazon.com) with any additional questions or comments.

## License

This project is licensed under the [Apache v2.0 License](LICENSE.txt).

## Copyright

Copyright OpenSearch Contributors. See [NOTICE](NOTICE.txt) for details.
