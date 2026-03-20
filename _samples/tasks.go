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
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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

	sourceIndex := "task-source"
	destIndex := "task-dest"

	// Create source index with test data.
	_, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"settings": {"number_of_shards": 1, "number_of_replicas": 0}}`),
	})
	if err != nil {
		return err
	}

	_, err = client.Index(ctx, opensearchapi.IndexReq{
		Index: sourceIndex,
		Body:  strings.NewReader(`{"title": "Test Document", "year": 2024}`),
		Params: opensearchapi.IndexParams{
			Refresh: "true",
		},
	})
	if err != nil {
		return err
	}

	// Submit an async reindex task.
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

	// Poll for completion.
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

	// Parse the BulkByScroll status.
	status, err := opensearchapi.ParseBulkByScrollTaskStatus(taskResp.Task.Status)
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

	// For tasks without a dedicated type, unmarshal as generic JSON.
	if taskResp.Task.Status != nil {
		var raw map[string]any
		if err := json.Unmarshal(taskResp.Task.Status, &raw); err != nil {
			return err
		}
		fmt.Printf("Raw status: %v\n", raw)
	}

	// List all running tasks.
	listResp, err := client.Tasks.List(ctx, nil)
	if err != nil {
		return err
	}
	for nodeID, node := range listResp.Nodes {
		fmt.Printf("Node %s (%s): %d tasks\n", node.Name, nodeID, len(node.Tasks))
	}

	// Cleanup.
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
