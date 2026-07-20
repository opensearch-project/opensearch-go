# Tasks

> **Runnable example:** [`_samples/usage-tasks.go`](../_samples/usage-tasks.go)

> **Note:** Examples in this guide use `opensearchutil.NewJSONReader` for request bodies that contain dynamic values. See [Security](config-security.md#request-body-construction) for details on safe body construction.

In this guide, you'll learn how to use the OpenSearch Golang Client API to manage asynchronous tasks. You'll learn how to submit long-running operations asynchronously, poll for their completion, and inspect task status.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v5/opensearchutil"
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
		Index:      sourceIndex,
		BodyReader: strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
	})

	// Index a document
	client.Doc.Index(ctx, opensearchapi.IndexReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"title": "Test Document", "year": 2024}`),
		Params: &opensearchapi.IndexParams{
			Refresh: "true",
		},
	})
```

## Submitting Async Tasks

Long-running operations like reindex, delete_by_query, and update_by_query can be submitted asynchronously by setting `WaitForCompletion` to `false`. The response body has a dynamic schema and is captured as raw JSON, so unmarshal it to read the task ID.

### Async Reindex

```go
	reindexResp, err := client.Reindex(ctx, &opensearchapi.ReindexReq{
		BodyReader: opensearchutil.NewJSONReader(map[string]any{
			"source": map[string]any{"index": sourceIndex},
			"dest":   map[string]any{"index": destIndex},
		}),
		Params: &opensearchapi.ReindexParams{
			WaitForCompletion: opensearch.ToPointer(false),
		},
	})
	if err != nil {
		return err
	}
	var reindexTask struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(reindexResp.Body, &reindexTask); err != nil {
		return err
	}
	taskID := reindexTask.Task
	fmt.Printf("Task submitted: %s\n", taskID)
```

## Polling for Completion

Use `Tasks.Get` to poll a task by ID. The `Completed` field indicates whether the task has finished. Use `require.Eventually` or a context deadline rather than a fixed sleep to avoid flaky behavior:

```go
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var taskResp *opensearchapi.TasksGetResp
	for {
		taskResp, err = client.Tasks.Get(ctx, opensearchapi.TasksGetReq{TaskID: taskID})
		if err != nil {
			return err
		}
		if taskResp.Completed {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for task %s", taskID)
		case <-time.After(500 * time.Millisecond):
		}
	}
	fmt.Printf("Task completed: action=%s\n", taskResp.Task.Action)
```

## Inspecting Task Status

The `Status` field on a task is a discriminated union (`*opensearchapi.TasksTaskInfoBaseStatus`) because its shape depends on the task type. Call `Type()` to determine which branch was decoded, then call the matching accessor. The raw JSON is always available via `RawJSON()`.

### BulkByScroll Tasks (reindex, delete_by_query, update_by_query)

For reindex, delete_by_query, and update_by_query tasks, call `BulkByScrollTaskStatus()` to get the typed struct. Note that `Created` and `Updated` are pointers and should be nil-checked:

```go
	status := taskResp.Task.Status.BulkByScrollTaskStatus()

	fmt.Printf("Total: %d\n", status.Total)
	if status.Created != nil {
		fmt.Printf("Created: %d\n", *status.Created)
	}
	if status.Updated != nil {
		fmt.Printf("Updated: %d\n", *status.Updated)
	}
	fmt.Printf("Deleted: %d\n", status.Deleted)
	fmt.Printf("Batches: %d\n", status.Batches)
	fmt.Printf("Version conflicts: %d\n", status.VersionConflicts)
	fmt.Printf("Noops: %d\n", status.Noops)
	fmt.Printf("Retries (bulk): %d\n", status.Retries.Bulk)
	fmt.Printf("Retries (search): %d\n", status.Retries.Search)
```

For sliced requests, the `Slices` field contains per-slice status. Each element is a `BulkByScrollTaskStatusSlicesItem` -- a discriminated union that is either a nested `BulkByScrollTaskStatus` (on success) or an `ErrorCause` (on failure). Call `Type()` then the matching accessor:

```go
	for i, slice := range status.Slices {
		switch slice.Type() {
		case opensearchapi.BulkByScrollTaskStatusSlicesItemBulkByScrollTaskStatusType:
			sliceStatus := slice.BulkByScrollTaskStatus()
			fmt.Printf("Slice %d: %d total\n", i, sliceStatus.Total)
		case opensearchapi.BulkByScrollTaskStatusSlicesItemExceptionType:
			exc := slice.Exception()
			reason := ""
			if exc.Reason != nil {
				reason = *exc.Reason
			}
			fmt.Printf("Slice %d failed: %s\n", i, reason)
		}
	}
```

### Replication Tasks

For replication tasks (e.g. index, delete, bulk shard operations), call `TasksReplicationTaskStatus()`:

```go
	replStatus := taskResp.Task.Status.TasksReplicationTaskStatus()
	fmt.Printf("Phase: %s\n", replStatus.Phase)
```

### Persistent Tasks

For persistent task executors, call `TasksPersistentTaskStatus()`:

```go
	persistStatus := taskResp.Task.Status.TasksPersistentTaskStatus()
	fmt.Printf("State: %s\n", persistStatus.State)
```

### Unknown Task Types

For task types without a dedicated branch, inspect the raw JSON directly:

```go
	if taskResp.Task.Status != nil {
		var raw map[string]any
		if err := json.Unmarshal(taskResp.Task.Status.RawJSON(), &raw); err != nil {
			return err
		}
		fmt.Printf("Raw status: %v\n", raw)
	}
```

## Listing Tasks

Use `Tasks.List` to see all running tasks on the cluster. The response body has a dynamic schema and is captured as raw JSON, so unmarshal it into a struct that matches the fields you need:

```go
	listResp, err := client.Tasks.List(ctx, nil)
	if err != nil {
		return err
	}
	var taskList struct {
		Nodes map[string]struct {
			Name  string         `json:"name"`
			Tasks map[string]any `json:"tasks"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(listResp.Body, &taskList); err != nil {
		return err
	}
	for nodeID, node := range taskList.Nodes {
		fmt.Printf("Node %s (%s): %d tasks\n", node.Name, nodeID, len(node.Tasks))
	}
```

## Cancelling Tasks

Long-running tasks can be cancelled by task ID. The response body has a dynamic schema and is captured as raw JSON:

```go
	cancelResp, err := client.Tasks.Cancel(ctx, opensearchapi.TasksCancelReq{TaskID: taskID})
	if err != nil {
		return err
	}
	var cancelled struct {
		Nodes map[string]struct {
			Tasks map[string]any `json:"tasks"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(cancelResp.Body, &cancelled); err != nil {
		return err
	}
	for _, node := range cancelled.Nodes {
		for id := range node.Tasks {
			fmt.Printf("Cancelled task: %s\n", id)
		}
	}
```

## Status Type Reference

The OpenSearch server returns different status structures depending on the task type. The `Status` field is a discriminated union (`*opensearchapi.TasksTaskInfoBaseStatus`); call `Type()` to determine the branch, then the matching accessor.

| Task Type                                 | Accessor                       | Status Struct                | Key Fields                                                                |
| ----------------------------------------- | ------------------------------ | ---------------------------- | ------------------------------------------------------------------------- |
| reindex, delete_by_query, update_by_query | `BulkByScrollTaskStatus()`     | `BulkByScrollTaskStatus`     | total, created, updated, deleted, batches, retries, throttle info, slices |
| replication (index, delete, bulk shard)   | `TasksReplicationTaskStatus()` | `TasksReplicationTaskStatus` | phase                                                                     |
| persistent task executor                  | `TasksPersistentTaskStatus()`  | `TasksPersistentTaskStatus`  | state                                                                     |

For any unrecognized task type, the raw JSON is available via `Status.RawJSON()` for direct unmarshaling, or `Status.Map()` for a `map[string]json.RawMessage`.

### Shorthand Helpers

If you read the same status type frequently, you can define a short helper in your own code:

```go
func bulkByScrollStatus(status *opensearchapi.TasksTaskInfoBaseStatus) opensearchapi.BulkByScrollTaskStatus {
	return status.BulkByScrollTaskStatus()
}
```

## Cleanup

```go
	delResp, err := client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
		Indices:  []string{sourceIndex, destIndex},
		Params: &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearch.ToPointer(true)},
	})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
```
