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
	"github.com/opensearch-project/opensearch-go/v4"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestNodesDNClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	config, err := ossectest.ClientConfig()
	require.Nil(t, err)

	clientTLSCert, err := tls.LoadX509KeyPair("../../admin.pem", "../../admin.key")
	require.Nil(t, err)

	config.Client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			Certificates:       []tls.Certificate{clientTLSCert},
		},
	}

	client, err := security.NewClient(*config)
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

	type nodesdnTests struct {
		Name    string
		Results func() (any, *opensearch.Response, error)
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
					Results: func() (any, *opensearch.Response, error) {
						return client.NodesDN.Put(
							nil,
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
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.NodesDN.Put(nil, security.NodesDNPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []nodesdnTests{
				{
					Name: "without request",
					Results: func() (any, *opensearch.Response, error) {
						return client.NodesDN.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.NodesDN.Get(nil, &security.NodesDNGetReq{Cluster: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.NodesDN.Get(nil, nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []nodesdnTests{
				{
					Name: "without request",
					Results: func() (any, *opensearch.Response, error) {
						return client.NodesDN.Delete(nil, security.NodesDNDeleteReq{Cluster: "test"})
					},
				},
				{
					Name: "inspect",
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.NodesDN.Delete(nil, security.NodesDNDeleteReq{Cluster: "test"})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []nodesdnTests{
				{
					Name: "with request",
					Results: func() (any, *opensearch.Response, error) {
						return client.NodesDN.Patch(
							nil,
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
					Results: func() (any, *opensearch.Response, error) {
						return failingClient.NodesDN.Patch(nil, security.NodesDNPatchReq{})
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					res, httpResp, err := testCase.Results()
					if testCase.Name == "inspect" {
						assert.NotNil(t, err)
						assert.NotNil(t, res)
						ossectest.VerifyResponse(t, httpResp)
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, httpResp)
						if value.Name != "Get" {
							ostest.CompareRawJSONwithParsedJSON(t, res, httpResp)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, httpResp, err := client.NodesDN.Get(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.DistinguishedNames, httpResp)
		})
	})
}
