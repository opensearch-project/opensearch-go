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

	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	client, err := opensearchapi.NewDefaultClient()
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
		_, err = client.Index(
			ctx,
			opensearchapi.IndexReq{
				Index:      exampleIndex,
				DocumentID: strconv.Itoa(i),
				Body:       strings.NewReader(fmt.Sprintf(`{"title": "The Dark Knight %d", "director": "Christopher Nolan", "year": %d}`, i, 2008+i)),
			},
		)
		if err != nil {
			return err
		}
	}

	_, err = client.Index(
		ctx,
		opensearchapi.IndexReq{
			Index: exampleIndex,
			Body:  strings.NewReader(`{"title": "The Godfather", "director": "Francis Ford Coppola", "year": 1972}`),
		},
	)
	if err != nil {
		return err
	}

	_, err = client.Index(
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
			Params:  opensearchapi.SearchParams{Query: `title: "dark knight"`},
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
			Params: opensearchapi.SearchParams{
				Query: `title: "dark knight"`,
				Size:  opensearchapi.ToPointer(2),
				From:  opensearchapi.ToPointer(5),
				Sort:  []string{"year:desc"},
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
			Params: opensearchapi.SearchParams{
				Query:  `title: "dark knight"`,
				Size:   opensearchapi.ToPointer(2),
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

	pitCreateResp, err := client.PointInTime.Create(
		ctx,
		opensearchapi.PointInTimeCreateReq{
			Indices: []string{exampleIndex},
			Params:  opensearchapi.PointInTimeCreateParams{KeepAlive: time.Minute},
		},
	)
	if err != nil {
		return err
	}

	searchResp, err = client.Search(
		ctx,
		&opensearchapi.SearchReq{
			Body: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" } }`, pitCreateResp.PitID)),
			Params: opensearchapi.SearchParams{
				Size: opensearchapi.ToPointer(5),
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
			Body: strings.NewReader(fmt.Sprintf(`{ "pit": { "id": "%s", "keep_alive": "1m" }, "search_after": [ "1994" ] }`, pitCreateResp.PitID)),
			Params: opensearchapi.SearchParams{
				Size: opensearchapi.ToPointer(5),
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

	_, err = client.PointInTime.Delete(ctx, opensearchapi.PointInTimeDeleteReq{PitID: []string{pitCreateResp.PitID}})
	if err != nil {
		return err
	}

	sourceResp, err := client.Document.Source(
		ctx,
		opensearchapi.DocumentSourceReq{
			Index:      "movies",
			DocumentID: "1",
			Params: opensearchapi.DocumentSourceParams{
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

	sourceResp, err = client.Document.Source(
		ctx,
		opensearchapi.DocumentSourceReq{
			Index:      "movies",
			DocumentID: "1",
			Params: opensearchapi.DocumentSourceParams{
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
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
