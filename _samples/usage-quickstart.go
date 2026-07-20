// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Quickstart CRUD sample: create an index, index documents, get one back,
// search, and delete. Mirrors the example in the top-level README.
//
// Learn more:
//   - Guide: https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-document_lifecycle.md
//   - API reference: https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
		Client: opensearch.Config{
			InsecureSkipVerify: true, // For testing only. Use certificate for validation.
			Addresses:          []string{"https://localhost:9200"},
			Username:           "admin", // For testing only. Don't store credentials in code.
			Password:           "myStrongPassword123!",
		},
	})
	if err != nil {
		return err
	}

	ctx := context.Background()

	// Create an index.
	if _, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index:      "movies",
		BodyReader: strings.NewReader(`{"settings": {"number_of_shards": 1}}`),
	}); err != nil {
		return err
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
			return err
		}
		ids = append(ids, indexed.ID)
	}

	// Get the first document back by its generated ID.
	got, err := client.Doc.Get(ctx, opensearchapi.GetReq{Index: "movies", ID: ids[0]})
	if err != nil {
		return err
	}
	fmt.Printf("get id=%s found=%v source=%q\n", got.ID, got.Found, got.Source)

	// Search for WarGames; with two documents indexed, this confirms we get the
	// right one back rather than just "a" result.
	search, err := client.Search(ctx, &opensearchapi.SearchReq{
		Indices:    []string{"movies"},
		BodyReader: strings.NewReader(`{"query": {"match": {"title": "WarGames"}}}`),
	})
	if err != nil {
		return err
	}
	fmt.Printf("%d hit(s)\n", len(search.Hits.Hits))
	for _, hit := range search.Hits.Hits {
		fmt.Printf("  source=%q\n", hit.Source)
	}

	// Delete both documents.
	for _, id := range ids {
		if _, err = client.Doc.Delete(ctx, opensearchapi.DeleteReq{Index: "movies", ID: id}); err != nil {
			return err
		}
	}

	// Clean up the index.
	if _, err = client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{Indices: []string{"movies"}}); err != nil {
		return err
	}

	return nil
}
