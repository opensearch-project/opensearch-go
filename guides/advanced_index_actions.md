# Advanced Index Actions

In this guide, we will look at some advanced index actions that are not covered in the [Index Lifecycle](index_lifecycle.md) guide.

## Setup

Let's create a client instance, and an index named `movies`:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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
```

## API Actions

### Clear index cache

You can clear the cache of an index or indices by using the `Indices.ClearCache()` action. The following example clears the cache of the `movies` index:

```go
	clearCachResp, err := client.Indices.ClearCache(ctx, &opensearchapi.IndicesClearCacheReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Cach cleared for %s shards\n", clearCacheResp.Shards.Total)
```

By default, the `Indices.ClearCache()` action clears all types of cache. To clear specific types of cache pass the the `query`, `fielddata`, or `request` parameter to the action:

```go
	clearCachResp, err := client.Indices.ClearCache(
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
	fmt.Printf("Cach cleared for %s shards\n", clearCacheResp.Shards.Total)
```

### Flush index

Sometimes you might want to flush an index or indices to make sure that all data in the transaction log is persisted to the index. To flush an index or indices use the `Indices.Flush()` action. The following example flushes the `movies` index:

```go
	flushResp, err := client.Indices.Flush(ctx, &opensearchapi.IndicesFlushReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Flushed shards: %d\n", flushResp.Shards.Total)
```

### Refresh index

You can refresh an index or indices to make sure that all changes are available for search. To refresh an index or indices use the `Indices.Refresh()` action:

```go
	refreshResp, err := client.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Indices: []string{exampleIndex}})
	if err != nil {
		return err
	}
	fmt.Printf("Refreshed shards: %d\n", refreshResp.Shards.Total)
```

### Open/Close index

You can close an index to prevent read and write operations on the index. A closed index does not have to maintain certain data structures that an opened index require, reducing the memory and disk space required by the index. The following example closes and reopens the `movies` index:

```go
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
```

### Force merge index

You can force merge an index or indices to reduce the number of segments in the index. This can be useful if you have a large number of small segments in the index. Merging segments reduces the memory footprint of the index. Do note that this action is resource intensive and it is only recommended for read-only indices. The following example force merges the `movies` index:

```go
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
```

### Clone index

You can clone an index to create a new index with the same mappings, data, and MOST of the settings. The source index must be in read-only state for cloning. The following example blocks write operations from `movies` index, clones the said index to create a new index named `movies_clone`, then re-enables write:

```go
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
            Body: strings.NewReader(`{"index":{"blocks":{"write":null}}}`),
        },
    )
	if err != nil {
		return err
	}
	fmt.Printf("Settings updated: %t\n", settingResp.Acknowledged)
```

### Split index

You can split an index into another index with more primary shards. The source index must be in read-only state for splitting. The following example create the read-only `books` index with 30 routing shards and 5 shards (which is divisible by 30), splits index into `bigger_books` with 10 shards (which is also divisible by 30), then re-enables write:

```go
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
```

## Cleanup

Let's delete all the indices we created in this guide:

```go
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
```
