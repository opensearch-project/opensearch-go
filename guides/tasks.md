# Tasks

In this guide, you'll learn how to use the OpenSearch Golang Client API to manage asynchronous tasks. You'll learn how to submit long-running operations asynchronously, poll for their completion, and inspect task status.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

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
```

Next, create a source index with some test data:

```go
	sourceIndex := "task-source"
	destIndex := "task-dest"

	client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
	})

	// Index a document
	client.Index(ctx, opensearchapi.IndexReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"title": "Test Document", "year": 2024}`),
		Params: opensearchapi.IndexParams{
			Refresh: "true",
		},
	})
```

## Submitting Async Tasks

Long-running operations like reindex, delete_by_query, and update_by_query can be submitted asynchronously by setting `WaitForCompletion` to `false`. The response contains a task ID that can be used to poll for completion.

### Async Reindex

```go
	reindexResp, err := client.Reindex(ctx, opensearchapi.ReindexReq{
		Body: strings.NewReader(fmt.Sprintf(
			`{"source":{"index":"%s"},"dest":{"index":"%s"}}`,
			sourceIndex, destIndex,
		)),
		Params: opensearchapi.ReindexParams{
			WaitForCompletion: opensearchapi.ToPointer(false),
		},
	})
	if err != nil {
		return err
	}
	taskID := reindexResp.Task
	fmt.Printf("Task submitted: %s\n", taskID)
```

## Polling for Completion

Use `Tasks.Get` to poll a task by ID. The `Completed` field indicates whether the task has finished.

```go
	var taskResp *opensearchapi.TasksGetResp
	for {
		taskResp, err = client.Tasks.Get(ctx, opensearchapi.TasksGetReq{TaskID: taskID})
		if err != nil {
			return err
		}
		if taskResp.Completed {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("Task completed: action=%s\n", taskResp.Task.Action)
```

## Inspecting Task Status

The `Status` field on a task is a `json.RawMessage` because its shape depends on the task type. The client provides typed structs and parse helpers for each known status type.

### BulkByScroll Tasks (reindex, delete_by_query, update_by_query)

For reindex, delete_by_query, and update_by_query tasks, use `ParseTaskStatus` to unmarshal the status into a typed struct:

```go
	status, err := opensearchapi.ParseTaskStatus[opensearchapi.BulkByScrollTaskStatus](taskResp.Task.Status)
	if err != nil {
		return err
	}

	fmt.Printf("Total: %d\n", status.Total)
	fmt.Printf("Created: %d\n", status.Created)
	fmt.Printf("Updated: %d\n", status.Updated)
	fmt.Printf("Deleted: %d\n", status.Deleted)
	fmt.Printf("Batches: %d\n", status.Batches)
	fmt.Printf("Version conflicts: %d\n", status.VersionConflicts)
	fmt.Printf("Noops: %d\n", status.Noops)
	fmt.Printf("Retries (bulk): %d\n", status.Retries.Bulk)
	fmt.Printf("Retries (search): %d\n", status.Retries.Search)
```

For sliced requests, the `Slices` field contains per-slice status. Each element is a `BulkByScrollTaskStatusOrException` — either a nested `BulkByScrollTaskStatus` (on success) or a `BulkByScrollTaskException` (on failure):

```go
	for i, slice := range status.Slices {
		if slice.Status != nil {
			fmt.Printf("Slice %d: %d total\n", i, slice.Status.Total)
		}
		if slice.Exception != nil {
			fmt.Printf("Slice %d failed: %s\n", i, slice.Exception.Reason)
		}
	}
```

### Replication Tasks

For replication tasks (e.g. index, delete, bulk shard operations), use `ParseTaskStatus`:

```go
	replStatus, err := opensearchapi.ParseTaskStatus[opensearchapi.ReplicationTaskStatus](taskResp.Task.Status)
	if err != nil {
		return err
	}
	fmt.Printf("Phase: %s\n", replStatus.Phase)
```

### Primary-Replica Resync Tasks

For primary-replica resync tasks, use `ParseTaskStatus`:

```go
	resyncStatus, err := opensearchapi.ParseTaskStatus[opensearchapi.ResyncTaskStatus](taskResp.Task.Status)
	if err != nil {
		return err
	}
	fmt.Printf("Phase: %s\n", resyncStatus.Phase)
	fmt.Printf("Total operations: %d\n", resyncStatus.TotalOperations)
	fmt.Printf("Resynced: %d\n", resyncStatus.ResyncedOperations)
	fmt.Printf("Skipped: %d\n", resyncStatus.SkippedOperations)
```

### Persistent Tasks

For persistent task executors, use `ParseTaskStatus`:

```go
	persistStatus, err := opensearchapi.ParseTaskStatus[opensearchapi.PersistentTaskStatus](taskResp.Task.Status)
	if err != nil {
		return err
	}
	fmt.Printf("State: %s\n", persistStatus.State)
```

### Unknown Task Types

For task types without a dedicated struct, unmarshal the raw JSON directly:

```go
	if taskResp.Task.Status != nil {
		var raw map[string]any
		if err := json.Unmarshal(taskResp.Task.Status, &raw); err != nil {
			return err
		}
		fmt.Printf("Raw status: %v\n", raw)
	}
```

## Listing Tasks

Use `Tasks.List` to see all running tasks on the cluster:

```go
	listResp, err := client.Tasks.List(ctx, nil)
	if err != nil {
		return err
	}
	for nodeID, node := range listResp.Nodes {
		fmt.Printf("Node %s (%s): %d tasks\n", node.Name, nodeID, len(node.Tasks))
	}
```

## Cancelling Tasks

Long-running tasks can be cancelled by task ID:

```go
	cancelResp, err := client.Tasks.Cancel(ctx, opensearchapi.TasksCancelReq{TaskID: taskID})
	if err != nil {
		return err
	}
	for _, node := range cancelResp.Nodes {
		for id := range node.Tasks {
			fmt.Printf("Cancelled task: %s\n", id)
		}
	}
```

## Status Type Reference

The OpenSearch server returns different status structures depending on the task type. All status types have been present since OpenSearch 1.0.0.

| Task Type                                 | Call                                      | Status Struct            | Key Fields                                                                |
| ----------------------------------------- | ----------------------------------------- | ------------------------ | ------------------------------------------------------------------------- |
| reindex, delete_by_query, update_by_query | `ParseTaskStatus[BulkByScrollTaskStatus]` | `BulkByScrollTaskStatus` | total, created, updated, deleted, batches, retries, throttle info, slices |
| replication (index, delete, bulk shard)   | `ParseTaskStatus[ReplicationTaskStatus]`  | `ReplicationTaskStatus`  | phase                                                                     |
| primary-replica resync                    | `ParseTaskStatus[ResyncTaskStatus]`       | `ResyncTaskStatus`       | phase, totalOperations, resyncedOperations, skippedOperations             |
| persistent task executor                  | `ParseTaskStatus[PersistentTaskStatus]`   | `PersistentTaskStatus`   | state                                                                     |

For any unrecognized task type, the `Status` field remains available as `json.RawMessage` for direct unmarshaling.

### Shorthand Helpers

If you parse the same status type frequently, you can define a short alias in your own code:

```go
func parseBulkByScrollStatus(raw json.RawMessage) (*opensearchapi.BulkByScrollTaskStatus, error) {
	return opensearchapi.ParseTaskStatus[opensearchapi.BulkByScrollTaskStatus](raw)
}
```

## Cleanup

```go
	delResp, err := client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{
		Indices: []string{sourceIndex, destIndex},
		Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
	})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
```
