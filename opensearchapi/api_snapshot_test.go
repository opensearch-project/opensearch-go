// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package opensearchapi_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v3/internal/test"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
	osapitest "github.com/opensearch-project/opensearch-go/v3/opensearchapi/internal/test"
)

func TestSnapshotClient(t *testing.T) {
	client, err := ostest.NewClient()
	require.Nil(t, err)
	failingClient, err := osapitest.CreateFailingClient()
	require.Nil(t, err)

	testRepo := "test-repository"
	testSnapshot := "test-snapshot"
	testCloneSnapshot := "test-snapshot-clone"
	testIndex := "test-snapshot"

	t.Cleanup(func() {
		client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
	})

	for i := 1; i <= 2; i++ {
		_, err = client.Document.Create(
			nil,
			opensearchapi.DocumentCreateReq{
				Index:      testIndex,
				Body:       strings.NewReader(`{"foo": "bar"}`),
				DocumentID: strconv.Itoa(i),
				Params:     opensearchapi.DocumentCreateParams{Refresh: "true"},
			},
		)
		require.Nil(t, err)
	}

	type snapshotTests struct {
		Name    string
		Results func() (osapitest.Response, error)
	}
	testCases := []struct {
		Name  string
		Tests []snapshotTests
	}{
		{
			Name: "Repository Create",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Repository.Create(
							nil,
							opensearchapi.SnapshotRepositoryCreateReq{
								Repo: testRepo,
								Body: strings.NewReader(`{"type":"fs","settings":{"location":"/usr/share/opensearch/mnt"}}`),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Repository.Create(nil, opensearchapi.SnapshotRepositoryCreateReq{})
					},
				},
			},
		},
		{
			Name: "Repository Get",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Repository.Get(nil, &opensearchapi.SnapshotRepositoryGetReq{Repos: []string{testRepo}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Repository.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Repository Cleanup",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Repository.Cleanup(nil, opensearchapi.SnapshotRepositoryCleanupReq{Repo: testRepo})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Repository.Cleanup(nil, opensearchapi.SnapshotRepositoryCleanupReq{})
					},
				},
			},
		},
		{
			Name: "Repository Verify",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Repository.Verify(nil, opensearchapi.SnapshotRepositoryVerifyReq{Repo: testRepo})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Repository.Verify(nil, opensearchapi.SnapshotRepositoryVerifyReq{})
					},
				},
			},
		},
		{
			Name: "Create",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Create(
							nil,
							opensearchapi.SnapshotCreateReq{
								Repo:     testRepo,
								Snapshot: testSnapshot,
								Body:     strings.NewReader(fmt.Sprintf(`{"indices":"%s"}`, testIndex)),
								Params:   opensearchapi.SnapshotCreateParams{WaitForCompletion: opensearchapi.ToPointer(true)},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Create(nil, opensearchapi.SnapshotCreateReq{})
					},
				},
			},
		},
		{
			Name: "Clone",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Clone(
							nil,
							opensearchapi.SnapshotCloneReq{
								Repo:           testRepo,
								Snapshot:       testSnapshot,
								TargetSnapshot: testCloneSnapshot,
								Body:           strings.NewReader(fmt.Sprintf(`{"indices":"%s"}`, testIndex)),
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Clone(nil, opensearchapi.SnapshotCloneReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Get(nil, opensearchapi.SnapshotGetReq{Repo: testRepo, Snapshots: []string{testSnapshot, testCloneSnapshot}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Get(nil, opensearchapi.SnapshotGetReq{})
					},
				},
			},
		},
		{
			Name: "Restore",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						client.Indices.Delete(nil, opensearchapi.IndicesDeleteReq{Indices: []string{testIndex}})
						return client.Snapshot.Restore(
							nil,
							opensearchapi.SnapshotRestoreReq{
								Repo:     testRepo,
								Snapshot: testSnapshot,
								Params:   opensearchapi.SnapshotRestoreParams{WaitForCompletion: opensearchapi.ToPointer(true)},
							})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Restore(nil, opensearchapi.SnapshotRestoreReq{})
					},
				},
			},
		},
		{
			Name: "Status",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Status(nil, opensearchapi.SnapshotStatusReq{Repo: testRepo, Snapshots: []string{testSnapshot, testCloneSnapshot}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Status(nil, opensearchapi.SnapshotStatusReq{})
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Delete(nil, opensearchapi.SnapshotDeleteReq{Repo: testRepo, Snapshots: []string{testSnapshot, testCloneSnapshot}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Delete(nil, opensearchapi.SnapshotDeleteReq{})
					},
				},
			},
		},
		{
			Name: "Repository Delete",
			Tests: []snapshotTests{
				{
					Name: "with request",
					Results: func() (osapitest.Response, error) {
						return client.Snapshot.Repository.Delete(nil, opensearchapi.SnapshotRepositoryDeleteReq{Repos: []string{testRepo}})
					},
				},
				{
					Name: "inspect",
					Results: func() (osapitest.Response, error) {
						return failingClient.Snapshot.Repository.Delete(nil, opensearchapi.SnapshotRepositoryDeleteReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						osapitest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Repository Get" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		_, err := client.Snapshot.Repository.Create(
			nil,
			opensearchapi.SnapshotRepositoryCreateReq{
				Repo: testRepo,
				Body: strings.NewReader(`{"type":"fs","settings":{"location":"/usr/share/opensearch/mnt"}}`),
			},
		)
		require.Nil(t, err)
		t.Cleanup(func() {
			client.Snapshot.Repository.Delete(nil, opensearchapi.SnapshotRepositoryDeleteReq{Repos: []string{testRepo}})
		})

		t.Run("Repository Get", func(t *testing.T) {
			resp, err := client.Snapshot.Repository.Get(nil, &opensearchapi.SnapshotRepositoryGetReq{Repos: []string{testRepo}})
			require.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Repos, resp.Inspect().Response)
		})
	})
}
