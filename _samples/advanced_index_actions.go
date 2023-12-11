// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	clearCacheResp, err := client.Indices.ClearCache(ctx, &opensearchapi.IndicesClearCacheReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Cach cleared for %d shards\n", clearCacheResp.Shards.Total)

	clearCacheResp, err = client.Indices.ClearCache(
		ctx,
		&opensearchapi.IndicesClearCacheReq{
			Indices: []string{exampleIndex},
			Params: opensearchapi.IndicesClearCacheParams{
				Fielddata: opensearchapi.ToPointer(true),
				Request:   opensearchapi.ToPointer(true),
				Query:     opensearchapi.ToPointer(true),
			},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Cach cleared for %d shards\n", clearCacheResp.Shards.Total)

	flushResp, err := client.Indices.Flush(ctx, &opensearchapi.IndicesFlushReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Flushed shards: %d\n", flushResp.Shards.Total)

	refreshResp, err := client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Refreshed shards: %d\n", refreshResp.Shards.Total)

	closeResp, err := client.Indices.Close(ctx, opensearchapi.IndicesCloseReq{Index: exampleIndex})
	if err != nil {
		return err
	}
	fmt.Printf("Index closed: %t\n", closeResp.Acknowledged)

	openResp, err := client.Indices.Open(ctx, opensearchapi.IndicesOpenReq{Index: exampleIndex})
	if err != nil {
		return err
	}
	fmt.Printf("Index opended: %t\n", openResp.Acknowledged)

	mergeResp, err := client.Indices.Forcemerge(
		ctx,
		&opensearchapi.IndicesForcemergeReq{
			Indices: []string{exampleIndex},
			Params: opensearchapi.IndicesForcemergeParams{
				MaxNumSegments: opensearchapi.ToPointer(1),
			},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Forcemerged Shards: %d\n", mergeResp.Shards.Total)

	blockResp, err := client.Indices.Block(
		ctx,
		opensearchapi.IndicesBlockReq{
			Indices: []string{exampleIndex},
			Block:   "write",
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index write blocked: %t\n", blockResp.Acknowledged)

	cloneResp, err := client.Indices.Clone(
		ctx,
		opensearchapi.IndicesCloneReq{
			Index:  exampleIndex,
			Target: "movies_cloned",
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Cloned: %t\n", cloneResp.Acknowledged)

	settingResp, err := client.Indices.Settings.Put(
		ctx,
		opensearchapi.SettingsPutReq{
			Indices: []string{exampleIndex},
			Body:    strings.NewReader(`{"index":{"blocks":{"write":null}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Settings updated: %t\n", settingResp.Acknowledged)

	createResp, err = client.Indices.Create(
		ctx,
		opensearchapi.IndicesCreateReq{
			Index: "books",
			Body: strings.NewReader(`{
        "settings": {
            "index": {
                "number_of_shards": 5,
                "number_of_routing_shards": 30,
                "blocks": {
                    "write": true
                }
            }
        }
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	splitResp, err := client.Indices.Split(
		ctx,
		opensearchapi.IndicesSplitReq{
			Index:  "books",
			Target: "books-large",
			Body:   strings.NewReader(`{"settings":{"index":{"number_of_shards": 10}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index splited: %t\n", splitResp.Acknowledged)

	settingResp, err = client.Indices.Settings.Put(
		ctx,
		opensearchapi.SettingsPutReq{
			Indices: []string{"books"},
			Body:    strings.NewReader(`{"index":{"blocks":{"write":null}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Settings updated: %t\n", settingResp.Acknowledged)

	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies*", "books*"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
