// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"encoding/json"
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
	// Initialize the client with SSL/TLS enabled.
	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				InsecureSkipVerify: true, // For testing only. Use certificate for validation.
				Addresses:          []string{"https://localhost:9200"},
				Username:           "admin", // For testing only. Don't store credentials in code.
				Password:           "myStrongPassword123!",
			},
		},
	)
	if err != nil {
		return err
	}

	ctx := context.Background()

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "movies"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	docCreateResp, err := client.Doc.Create(
		ctx,
		opensearchapi.CreateReq{
			Index: "movies",
			ID:    "1",
			Body:  strings.NewReader(`{"title": "Beauty and the Beast", "year": 1991 }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", docCreateResp.Result)

	docCreateResp, err = client.Doc.Create(
		ctx,
		opensearchapi.CreateReq{
			Index: "movies",
			ID:    "2",
			Body:  strings.NewReader(`{"title": "Beauty and the Beast - Live Action", "year": 2017 }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", docCreateResp.Result)

	_, err = client.Doc.Create(
		ctx,
		opensearchapi.CreateReq{
			Index: "movies",
			ID:    "2",
			Body:  strings.NewReader(`{"title": "Just Another Movie" }`),
		},
	)
	if err != nil {
		fmt.Println(err)
	}

	indexResp, err := client.Doc.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: "movies",
			ID:    "2",
			Body:  strings.NewReader(`{"title": "Updated Title" }`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Document: %s\n", indexResp.Result)

	//

	indexResp, err = client.Doc.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: "movies",
			Body:  strings.NewReader(`{ "title": "The Lion King 2", "year": 1978}`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(indexResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Index:\n%s\n", respAsJson)

	//

	getResp, err := client.Doc.Get(ctx, opensearchapi.GetReq{Index: "movies", ID: "1"})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)

	//

	getResp, err = client.Doc.Get(
		ctx,
		opensearchapi.GetReq{
			Index:  "movies",
			ID:     "1",
			Params: &opensearchapi.GetParams{SourceIncludes: []string{"title"}},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)

	getResp, err = client.Doc.Get(
		ctx,
		opensearchapi.GetReq{
			Index:  "movies",
			ID:     "1",
			Params: &opensearchapi.GetParams{SourceExcludes: []string{"title"}},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", respAsJson)

	//

	mgetResp, err := client.Doc.MGet(
		ctx,
		opensearchapi.MGetReq{
			Index:      "movies",
			BodyReader: strings.NewReader(`{ "docs": [{ "_id": "1" }, { "_id": "2" }] }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(mgetResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("MGet Document:\n%s\n", respAsJson)

	//

	existsResp, err := client.Doc.Exists(ctx, opensearchapi.ExistsReq{Index: "movies", ID: "1"})
	if err != nil {
		return err
	}
	fmt.Println(existsResp.Status())

	//

	updateResp, err := client.Doc.Update(
		ctx,
		opensearchapi.UpdateReq{
			Index:      "movies",
			ID:         "1",
			BodyReader: strings.NewReader(`{ "script": { "source": "ctx._source.year += 5" } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(updateResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Update:\n%s\n", respAsJson)

	//

	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Index: []string{"movies"}})
	if err != nil {
		return err
	}

	upByQueryResp, err := client.UpdateByQuery(
		ctx,
		&opensearchapi.UpdateByQueryReq{
			Index:      []string{"movies"},
			Params:     &opensearchapi.UpdateByQueryParams{Q: "year:<1990"},
			BodyReader: strings.NewReader(`{"script": { "source": "ctx._source.year -= 1" } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(upByQueryResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("UpdateByQuery:\n%s\n", respAsJson)

	//

	docDelResp, err := client.Doc.Delete(ctx, opensearchapi.DeleteReq{Index: "movies", ID: "1"})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(docDelResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Del Doc:\n%s\n", respAsJson)

	//
	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Index: []string{"movies"}})
	if err != nil {
		return err
	}

	delByQueryResp, err := client.DeleteByQuery(
		ctx,
		&opensearchapi.DeleteByQueryReq{
			Index:      []string{"movies"},
			BodyReader: strings.NewReader(`{ "query": { "match": { "title": "The Lion King" } } }`),
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(delByQueryResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("DelByQuery Doc:\n%s\n", respAsJson)

	//

	delResp, err := client.Indices.Delete(
		ctx,
		&opensearchapi.IndicesDeleteReq{
			Index:  []string{"movies", "paintings", "burner"},
			Params: &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearch.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
