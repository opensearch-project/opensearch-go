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
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Printf("Error: %s\n", err)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	sourceIndex := "task-source"
	destIndex := "task-dest"

	// Create source index with test data.
	_, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index:      sourceIndex,
		BodyReader: strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
	})
	if err != nil {
		return err
	}

	_, err = client.Doc.Index(ctx, opensearchapi.IndexReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"title": "Test Document", "year": 2024}`),
		Params: &opensearchapi.IndexParams{
			Refresh: "true",
		},
	})
	if err != nil {
		return err
	}

	// Submit an async reindex task.
	reindexResp, err := client.Reindex(ctx, &opensearchapi.ReindexReq{
		BodyReader: strings.NewReader(fmt.Sprintf(
			`{"source":{"index":"%s"},"dest":{"index":"%s"}}`,
			sourceIndex, destIndex,
		)),
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

	// Poll for completion.
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

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
			return ctx.Err()
		case <-ticker.C:
		}
	}
	fmt.Printf("Task completed: action=%s\n", taskResp.Task.Action)

	// Read the BulkByScroll status.
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

	// For tasks without a dedicated type, inspect the raw JSON.
	if taskResp.Task.Status != nil {
		var raw map[string]any
		if err := json.Unmarshal(taskResp.Task.Status.RawJSON(), &raw); err != nil {
			return err
		}
		fmt.Printf("Raw status: %v\n", raw)
	}

	// List all running tasks.
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

	// Cleanup.
	delResp, err := client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
		Index:  []string{sourceIndex, destIndex},
		Params: &opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearch.ToPointer(true)},
	})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
