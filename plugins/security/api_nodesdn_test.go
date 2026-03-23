// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityNodesDNClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	config := ossectest.ClientConfig(t)

	clientTLSCert, err := tls.LoadX509KeyPair("../../admin.pem", "../../admin.key")
	require.NoError(t, err)

	config.Client.InsecureSkipVerify = true
	tp := http.DefaultTransport.(*http.Transport).Clone()
	tp.TLSClientConfig.Certificates = []tls.Certificate{clientTLSCert}
	config.Client.Transport = tp

	client, err := security.NewClient(*config)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient(t)
	require.NoError(t, err)

	testCluster := testutil.MustUniqueString(t, "test-cluster")
	t.Cleanup(func() {
		client.NodesDN.Delete(context.Background(), security.NodesDNDeleteReq{Cluster: testCluster})
	})

	type nodesdnTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []nodesdnTests
	}{
		{
			Name: "Put",
			Tests: []nodesdnTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.NodesDN.Put(
							t.Context(),
							security.NodesDNPutReq{
								Cluster: testCluster,
								Body: security.NodesDNPutBody{
									NodesDN: []string{"CN=test.example.com"},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.NodesDN.Put(t.Context(), security.NodesDNPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []nodesdnTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.NodesDN.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.NodesDN.Get(t.Context(), &security.NodesDNGetReq{Cluster: testCluster})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.NodesDN.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []nodesdnTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.NodesDN.Delete(t.Context(), security.NodesDNDeleteReq{Cluster: testCluster})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.NodesDN.Delete(t.Context(), security.NodesDNDeleteReq{Cluster: testCluster})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []nodesdnTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.NodesDN.Patch(
							t.Context(),
							security.NodesDNPatchReq{
								Body: security.NodesDNPatchBody{
									security.NodesDNPatchBodyItem{
										OP:   "add",
										Path: fmt.Sprintf("/%s", testCluster),
										Value: security.NodesDNPutBody{
											NodesDN: []string{"CN=test.example.com"},
										},
									},
									security.NodesDNPatchBodyItem{
										OP:   "remove",
										Path: fmt.Sprintf("/%s", testCluster),
									},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.NodesDN.Patch(t.Context(), security.NodesDNPatchReq{})
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
						require.Error(t, err)
						assert.NotNil(t, res)
						ossectest.VerifyInspect(t, res.Inspect())
					} else {
						require.NoError(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" {
							testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, err := client.NodesDN.Get(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.DistinguishedNames, resp.Inspect().Response)
		})
	})
}
