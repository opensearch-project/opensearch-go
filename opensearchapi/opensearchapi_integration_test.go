// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// +build integration

package opensearchapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

func createTestIndex(client *opensearch.Client, index string) error {
	var buf bytes.Buffer
	// Index data
	//
	for j := 1; j <= 1000; j++ {
		meta := []byte(fmt.Sprintf(`{ "index" : { "_id" : "%d" } }%s`, j, "\n"))
		data := []byte(`{"content":"` + strings.Repeat("ABC", 100) + `"}`)
		data = append(data, "\n"...)

		buf.Grow(len(meta) + len(data))
		buf.Write(meta)
		buf.Write(data)
	}
	_, err := client.Bulk(bytes.NewReader(buf.Bytes()), client.Bulk.WithIndex(index), client.Bulk.WithRefresh("true"))
	return err
}

func TestAPI(t *testing.T) {
	t.Run("Search", func(t *testing.T) {
		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		client.Cluster.Health(client.Cluster.Health.WithWaitForStatus("yellow"))
		res, err := client.Search(client.Search.WithTimeout(500 * time.Millisecond))
		if err != nil {
			t.Fatalf("Error getting the response: %s\n", err)
		}
		defer res.Body.Close()

		if res.IsError() {
			t.Fatalf("Error response: %s", res.String())
		}

		var d map[string]interface{}
		err = json.NewDecoder(res.Body).Decode(&d)
		if err != nil {
			t.Fatalf("Error parsing the response: %s\n", err)
		}
		fmt.Printf("took=%vms\n", d["took"])
	})

	t.Run("Headers", func(t *testing.T) {
		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		res, err := client.Info(client.Info.WithHeader(map[string]string{"Accept": "application/yaml"}))
		if err != nil {
			t.Fatalf("Error getting the response: %s\n", err)
		}
		defer res.Body.Close()

		if res.IsError() {
			t.Fatalf("Error response: %s", res.String())
		}

		if !strings.HasPrefix(res.String(), "[200 OK] ---") {
			t.Errorf("Unexpected response body: doesn't start with '[200 OK] ---'; %s", res.String())
		}
	})

	t.Run("OpaqueID", func(t *testing.T) {
		var (
			res *opensearchapi.Response
			err error

			requestID = "reindex-123"
		)

		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		// Prepare indices
		//
		client.Indices.Delete([]string{"test", "reindexed"}, client.Indices.Delete.WithIgnoreUnavailable(true))

		// Index data
		//
		err = createTestIndex(client, "test")
		if err != nil {
			t.Fatalf("Failed to index data: %s", err)
		}

		// Launch reindexing task with wait_for_completion=false
		//
		res, err = client.Reindex(
			strings.NewReader(`{"source":{"index":"test"}, "dest": {"index":"reindexed"}}`),
			client.Reindex.WithWaitForCompletion(false),
			client.Reindex.WithRequestsPerSecond(1),
			client.Reindex.WithOpaqueID(requestID))
		if err != nil {
			t.Fatalf("Failed to reindex: %s", err)
		}
		if res.IsError() {
			t.Fatalf("Failed to reindex: %s", res.Status())
		}
		time.Sleep(10 * time.Millisecond)

		res, err = client.Tasks.List(client.Tasks.List.WithPretty())
		if err != nil {
			t.Fatalf("ERROR: %s", err)
		}
		res.Body.Close()
		if res.IsError() {
			t.Fatalf("Failed to get tasks: %s", res.Status())
		}

		// Get the list of tasks
		//
		res, err = client.Tasks.List(client.Tasks.List.WithPretty())
		if err != nil {
			t.Fatalf("ERROR: %s", err)
		}
		defer res.Body.Close()

		if res.IsError() {
			t.Fatalf("Failed to get tasks: %s", res.Status())
		}

		type task struct {
			Node        string
			ID          int
			Action      string
			RunningTime time.Duration `json:"running_time_in_nanos"`
			Cancellable bool
			Headers     map[string]interface{}
		}

		type node struct {
			Tasks map[string]task
		}

		var nodes map[string]map[string]node
		if err := json.NewDecoder(res.Body).Decode(&nodes); err != nil {
			t.Fatalf("Failed to decode response: %s", err)
		}

		var hasReindexTask bool

		for _, n := range nodes["nodes"] {
			for taskID, task := range n.Tasks {
				if task.Headers["X-Opaque-Id"] == requestID {
					if strings.Contains(task.Action, "reindex") {
						hasReindexTask = true
					}
					fmt.Printf("* %s, %s | %s (%s)\n", requestID, taskID, task.Action, task.RunningTime)
				}
			}
		}

		if !hasReindexTask {
			t.Errorf("Expected reindex task in %+v", nodes["nodes"])
		}

		for _, n := range nodes["nodes"] {
			for taskID, task := range n.Tasks {
				if task.Headers["X-Opaque-Id"] == requestID {
					if task.Cancellable {
						fmt.Printf("=> Closing task %s\n", taskID)
						res, err = client.Tasks.Cancel(client.Tasks.Cancel.WithTaskID(taskID))
						if err != nil {
							t.Fatalf("ERROR: %s", err)
						}
						res.Body.Close()
						if res.IsError() {
							t.Fatalf("Failed to cancel task: %s", res)
						}
					}
				}
			}
		}
	})
	t.Run("Snapshot", func(t *testing.T) {
		// Functio to perform requests
		//
		opensearchDo := func(ctx context.Context, client *opensearch.Client, req opensearchapi.Request, msg string, t *testing.T) {
			_, err := req.Do(ctx, client)
			if err != nil {
				t.Fatalf("Error performing the request: %s\n", err)
			}
		}

		// Create Client
		//
		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		// Pre Cleanup indices
		//
		iDeleteReq := &opensearchapi.IndicesDeleteRequest{
			Index:             []string{"test", "test_restored"},
			IgnoreUnavailable: opensearchapi.BoolPtr(true),
		}
		ctx := context.Background()
		opensearchDo(ctx, client, iDeleteReq, "index data", t)

		// Index data
		//
		err = createTestIndex(client, "test")
		if err != nil {
			t.Fatalf("Failed to index data: %s", err)
		}

		// Test Snapshot functions
		//
		sRepoCreateReq := &opensearchapi.SnapshotCreateRepositoryRequest{
			Body:       bytes.NewBufferString(`{"type":"fs","settings":{"location":"/usr/share/opensearch/mnt"}}`),
			Repository: "snapshot-test",
		}
		opensearchDo(ctx, client, sRepoCreateReq, "create Snapshot Repository", t)

		sRepoVerifyReq := &opensearchapi.SnapshotVerifyRepositoryRequest{
			Repository: "snapshot-test",
		}
		opensearchDo(ctx, client, sRepoVerifyReq, "verify Snapshot Repository", t)

		sDeleteReq := &opensearchapi.SnapshotDeleteRequest{
			Snapshot:   []string{"test", "clone-test"},
			Repository: "snapshot-test",
		}
		opensearchDo(ctx, client, sDeleteReq, "delete Snapshots", t)

		sCreateReq := &opensearchapi.SnapshotCreateRequest{
			Body:              bytes.NewBufferString(`{"indices":"test","ignore_unavailable":true,"include_global_state":false,"partial":false}`),
			Snapshot:          "test",
			Repository:        "snapshot-test",
			WaitForCompletion: opensearchapi.BoolPtr(true),
		}
		opensearchDo(ctx, client, sCreateReq, "create Snapshot", t)

		sCloneReq := &opensearchapi.SnapshotCloneRequest{
			Body:           bytes.NewBufferString(`{"indices":"*"}`),
			Snapshot:       "test",
			TargetSnapshot: "clone-test",
			Repository:     "snapshot-test",
		}
		opensearchDo(ctx, client, sCloneReq, "clone Snapshot", t)

		sGetReq := &opensearchapi.SnapshotGetRequest{
			Snapshot:   []string{"test", "clone-test"},
			Repository: "snapshot-test",
		}
		opensearchDo(ctx, client, sGetReq, "get Snapshots", t)

		sStatusReq := &opensearchapi.SnapshotGetRequest{
			Snapshot:   []string{"test", "clone-test"},
			Repository: "snapshot-test",
		}
		opensearchDo(ctx, client, sStatusReq, "get Snapshot status", t)

		sRestoreReq := &opensearchapi.SnapshotRestoreRequest{
			Body: bytes.NewBufferString(
				`{
					"indices":"test",
					"ignore_unavailable":true,
					"include_global_state":false,
					"partial":false,
					"rename_pattern": "(.+)",
					"rename_replacement":"$1_restored"
				}`,
			),
			Snapshot:          "clone-test",
			Repository:        "snapshot-test",
			WaitForCompletion: opensearchapi.BoolPtr(true),
		}
		opensearchDo(ctx, client, sRestoreReq, "restore Snapshot", t)

		opensearchDo(ctx, client, sDeleteReq, "delete Snapshots", t)
		opensearchDo(ctx, client, iDeleteReq, "index data", t)
	})
	t.Run("Point_in_Time", func(t *testing.T) {
		var (
			err          error
			major, minor int64
			data         opensearchapi.InfoResp
		)
		index := "test"

		// Create Client
		//
		client, err := opensearch.NewDefaultClient()
		if err != nil {
			t.Fatalf("Error creating the client: %s\n", err)
		}

		// Skip test if Cluster version is below 2.4.0
		infoResp, err := client.Info()
		if err != nil {
			t.Fatalf("Error getting the cluster info: %s\n", err)
		}
		if err = json.NewDecoder(infoResp.Body).Decode(&data); err != nil {
			t.Fatalf("Error parsing the cluster info: %s\n", err)
		}
		major, minor, _, err = opensearch.ParseVersion(data.Version.Number)
		if err != nil {
			t.Fatalf("Error parsing the cluster version")
		}
		if major <= 2 && minor < 4 {
			return
		}

		// Cleanup all existing Pits
		//
		resp, _, err := client.PointInTime.Delete(client.PointInTime.Delete.WithPitID("_all"))
		if err != nil {
			if resp != nil && resp.StatusCode != 404 {
				t.Fatalf("Failed to Delete all Pits: %s", err)
			}
		}

		// Index data
		//
		err = createTestIndex(client, index)
		if err != nil {
			t.Fatalf("Failed to index data: %s", err)
		}

		// Create a Pit
		//
		keepAlive, _ := time.ParseDuration("5m")
		_, pitCreateResp, err := client.PointInTime.Create(client.PointInTime.Create.WithKeepAlive(keepAlive), client.PointInTime.Create.WithIndex(index))
		if err != nil {
			t.Fatalf("Failed to create Pit: %s", err)
		}

		// Get all Pits
		//
		_, pitGetResp, err := client.PointInTime.Get()
		if err != nil {
			t.Fatalf("Failed to get Pits: %s", err)
		}

		// Delete the create Pit
		//
		_, pitDeleteResp, err := client.PointInTime.Delete(client.PointInTime.Delete.WithPitID(pitCreateResp.PitID))
		if err != nil {
			t.Fatalf("Failed to delete Pit: %s", err)
		}
		if (pitCreateResp.PitID != pitGetResp.Pits[0].PitID) || (pitCreateResp.PitID != pitDeleteResp.Pits[0].PitID) {
			t.Fatalf("The create Pit does not match the Get Pit or Deleted Pit")
		}
	})
}
