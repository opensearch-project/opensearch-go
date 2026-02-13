// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityNodesDNClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	config, err := ossectest.ClientConfig(t)
	require.NoError(t, err)

	clientTLSCert, err := tls.LoadX509KeyPair("../../admin.pem", "../../admin.key")
	require.NoError(t, err)

	config.Client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{clientTLSCert},
		},
	}

	client, err := security.NewClient(*config)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

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
								Cluster: "test",
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
						return client.NodesDN.Get(t.Context(), &security.NodesDNGetReq{Cluster: "test"})
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
						return client.NodesDN.Delete(t.Context(), security.NodesDNDeleteReq{Cluster: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.NodesDN.Delete(t.Context(), security.NodesDNDeleteReq{Cluster: "test"})
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
										Path: "/test",
										Value: security.NodesDNPutBody{
											NodesDN: []string{"CN=test.example.com"},
										},
									},
									security.NodesDNPatchBodyItem{
										OP:   "remove",
										Path: "/test",
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
