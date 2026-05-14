// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/plugins/mlcommons"
)

func main() {
	if err := example(); err != nil {
		fmt.Printf("Error: %s\n", err)
		os.Exit(1)
	}
}

func example() error {
	// The ML Commons plugin must be enabled on the cluster, and node settings
	// (e.g. plugins.ml_commons.only_run_on_ml_node=false for single-node dev clusters)
	// configured before register/deploy succeed.
	client, err := mlcommons.NewClient(mlcommons.Config{
		Client: opensearch.Config{
			InsecureSkipVerify: true, // For testing only. Use certificate for validation.
			Addresses:          []string{"https://localhost:9200"},
			Username:           "admin", // For testing only. Don't store credentials in code.
			Password:           "myStrongPassword123!",
		},
	})
	if err != nil {
		return err
	}

	ctx := context.Background()

	// 1. Register a pretrained sentence-transformer.
	deploy := false
	registerResp, err := client.Models.Register(ctx, mlcommons.ModelsRegisterReq{
		Body: mlcommons.ModelsRegisterBody{
			Name:         "huggingface/sentence-transformers/all-MiniLM-L6-v2",
			Version:      "1.0.1",
			ModelFormat:  "TORCH_SCRIPT",
			FunctionName: "TEXT_EMBEDDING",
			Deploy:       &deploy,
		},
	})
	if err != nil {
		return fmt.Errorf("register: %w", err)
	}
	fmt.Printf("registered: task_id=%s\n", registerResp.TaskID)

	modelID, err := waitForTaskModelID(ctx, client, registerResp.TaskID, 3*time.Minute)
	if err != nil {
		return fmt.Errorf("waiting for register task: %w", err)
	}
	fmt.Printf("model registered: model_id=%s\n", modelID)

	// 2. Deploy.
	deployResp, err := client.Models.Deploy(ctx, mlcommons.ModelsDeployReq{ModelID: modelID})
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	fmt.Printf("deploying: task_id=%s\n", deployResp.TaskID)
	if _, err := waitForTaskCompletion(ctx, client, deployResp.TaskID, 3*time.Minute); err != nil {
		return fmt.Errorf("waiting for deploy task: %w", err)
	}

	// 3. Predict.
	predictBody, _ := json.Marshal(map[string]any{
		"text_docs": []string{"OpenSearch is a community-driven search engine."},
	})
	predictResp, err := client.Models.Predict(ctx, mlcommons.ModelsPredictReq{
		ModelID: modelID,
		Body:    predictBody,
	})
	if err != nil {
		return fmt.Errorf("predict: %w", err)
	}
	fmt.Printf("predict result (truncated): %s...\n", truncate(string(predictResp.InferenceResults), 120))

	// 4. Undeploy + delete.
	if _, err := client.Models.Undeploy(ctx, mlcommons.ModelsUndeployReq{ModelID: modelID}); err != nil {
		return fmt.Errorf("undeploy: %w", err)
	}
	if _, err := client.Models.Delete(ctx, mlcommons.ModelsDeleteReq{ModelID: modelID}); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	fmt.Println("cleanup complete")
	return nil
}

// waitForTaskModelID polls Tasks.Get until the task is COMPLETED (or fails / times out) and
// returns the resulting model_id. Used after register; deploy tasks do not yield a new model_id.
func waitForTaskModelID(ctx context.Context, c *mlcommons.Client, taskID string, timeout time.Duration) (string, error) {
	resp, err := waitForTaskCompletion(ctx, c, taskID, timeout)
	if err != nil {
		return "", err
	}
	if resp.ModelID == "" {
		return "", errors.New("task completed but model_id is empty")
	}
	return resp.ModelID, nil
}

func waitForTaskCompletion(ctx context.Context, c *mlcommons.Client, taskID string, timeout time.Duration) (mlcommons.TasksGetResp, error) {
	deadline := time.Now().Add(timeout)
	for {
		resp, err := c.Tasks.Get(ctx, mlcommons.TasksGetReq{TaskID: taskID})
		if err != nil {
			return resp, err
		}
		switch resp.State {
		case "COMPLETED":
			return resp, nil
		case "FAILED", "CANCELLED":
			return resp, fmt.Errorf("task %s ended in state %s: %s", taskID, resp.State, resp.Error)
		}
		if time.Now().After(deadline) {
			return resp, fmt.Errorf("timed out waiting for task %s (last state: %s)", taskID, resp.State)
		}
		select {
		case <-ctx.Done():
			return resp, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
