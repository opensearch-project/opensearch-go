// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// This sample demonstrates clear cache, flush, refresh, force merge, and other index actions.
//
// Learn more:
//   - Guide: https://github.com/opensearch-project/opensearch-go/blob/main/guides/indexing-advanced_index_actions.md
//   - API reference: https://pkg.go.dev/github.com/opensearch-project/opensearch-go/v5/opensearchapi

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
			Params: &opensearchapi.IndicesClearCacheParams{
				Fielddata: opensearch.ToPointer(true),
				Request:   opensearch.ToPointer(true),
				Query:     opensearch.ToPointer(true),
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

	closeResp, err := client.Indices.Close(ctx, &opensearchapi.IndicesCloseReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Index closed: %t\n", closeResp.Acknowledged)

	openResp, err := client.Indices.Open(ctx, &opensearchapi.IndicesOpenReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	openRespJSON, err := json.MarshalIndent(openResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Index opened:\n%s\n", string(openRespJSON))

	mergeResp, err := client.Indices.ForceMerge(
		ctx,
		&opensearchapi.IndicesForceMergeReq{
			Indices: []string{exampleIndex},
			Params: &opensearchapi.IndicesForceMergeParams{
				MaxNumSegments: 1,
			},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Forcemerged Shards: %d\n", mergeResp.Shards.Total)

	blockResp, err := client.Indices.AddBlock(
		ctx,
		opensearchapi.IndicesAddBlockReq{
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
		&opensearchapi.IndicesPutSettingsReq{
			Indices:    []string{exampleIndex},
			BodyReader: strings.NewReader(`{"index":{"blocks":{"write":null}}}`),
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
			BodyReader: strings.NewReader(`{
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
			Index:      "books",
			Target:     "books-large",
			BodyReader: strings.NewReader(`{"settings":{"index":{"number_of_shards": 10}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index splited: %t\n", splitResp.Acknowledged)

	settingResp, err = client.Indices.Settings.Put(
		ctx,
		&opensearchapi.IndicesPutSettingsReq{
			Indices:    []string{"books"},
			BodyReader: strings.NewReader(`{"index":{"blocks":{"write":null}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Settings updated: %t\n", settingResp.Acknowledged)

	delResp, err := client.Indices.Delete(
		ctx,
		&opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies*", "books*"},
			Params:  &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearch.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
