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
	"strconv"
	"strings"
	"time"

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
	exampleIndex := "movies"

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: exampleIndex})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	for i := 1; i < 11; i++ {
		_, err = client.Doc.Index(
			ctx,
			opensearchapi.IndexReq{
				Index: exampleIndex,
				ID:    strconv.Itoa(i),
				Body:  strings.NewReader(fmt.Sprintf(`{"title": "The Dark Knight %d", "director": "Christopher Nolan", "year": %d}`, i, 2008+i)),
			},
		)
		if err != nil {
			return err
		}
	}

	_, err = client.Doc.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Godfather", "director": "Francis Ford Coppola", "year": 1972}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Doc.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Shawshank Redemption", "director": "Frank Darabont", "year": 1994}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}

	searchResp, err := client.Search(ctx, &opensearchapi.SearchReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	//

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params:  &opensearchapi.SearchParams{Q: `title: "dark knight"`},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	//

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.SearchParams{
				Q:    `title: "dark knight"`,
				Size: opensearch.ToPointer(2),
				From: 5,
				Sort: []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	//

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.SearchParams{
				Q:      `title: "dark knight"`,
				Size:   opensearch.ToPointer(2),
				Sort:   []string{"year:desc"},
				Scroll: time.Minute,
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	//

	pitCreateResp, err := client.PIT.Create(
		ctx,
		&opensearchapi.CreatePITReq{
			Indices: []string{exampleIndex},
			Params:  &opensearchapi.CreatePITParams{KeepAlive: time.Minute},
		},
	)
	if err != nil {
		return err
	}
	pitID := ""
	if pitCreateResp.PITID != nil {
		pitID = *pitCreateResp.PITID
	}

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			BodyReader: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" } }`, pitID)),
			Params: &opensearchapi.SearchParams{
				Size: opensearch.ToPointer(5),
				Sort: []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			BodyReader: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" }, "search_after": [ "1994" ] }`, pitID)),
			Params: &opensearchapi.SearchParams{
				Size: opensearch.ToPointer(5),
				Sort: []string{"year:desc"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(searchResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Search Response:\n%s\n", string(respAsJson))

	_, err = client.PIT.Delete(ctx, &opensearchapi.DeletePITReq{Body: &opensearchapi.DeletePITBody{PITID: []string{pitID}}})
	if err != nil {
		return err
	}

	sourceResp, err := client.Doc.GetSource(
		ctx,
		opensearchapi.GetSourceReq{
			Index: "movies",
			ID:    "1",
			Params: &opensearchapi.GetSourceParams{
				SourceIncludes: []string{"title"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(sourceResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Source Response:\n%s\n", string(respAsJson))

	sourceResp, err = client.Doc.GetSource(
		ctx,
		opensearchapi.GetSourceReq{
			Index: "movies",
			ID:    "1",
			Params: &opensearchapi.GetSourceParams{
				SourceExcludes: []string{"title"},
			},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(sourceResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Source Response:\n%s\n", string(respAsJson))

	delResp, err := client.Indices.Delete(
		ctx,
		&opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies"},
			Params:  &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearch.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
