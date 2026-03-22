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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityConfigClient(t *testing.T) {
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

	type securityconfigTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []securityconfigTests
	}{
		{
			Name: "Get",
			Tests: []securityconfigTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.SecurityConfig.Get(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SecurityConfig.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Put",
			Tests: []securityconfigTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						// Re-fetch the config to get the current seqNo/primaryTerm.
						// The security config may have been modified by concurrent
						// tests or background operations since the initial Get.
						getResp, err := client.SecurityConfig.Get(t.Context(), nil)
						if err != nil {
							return getResp, err
						}
						return client.SecurityConfig.Put(
							t.Context(),
							security.ConfigPutReq{
								Body: security.ConfigPutBody{Dynamic: getResp.Config.Dynamic},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SecurityConfig.Put(t.Context(), security.ConfigPutReq{})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []securityconfigTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.SecurityConfig.Patch(
							t.Context(),
							security.ConfigPatchReq{
								Body: security.ConfigPatchBody{
									security.ConfigPatchBodyItem{
										OP:    "replace",
										Path:  "/config/dynamic/authc/basic_internal_auth_domain/http_enabled",
										Value: false,
									},
									security.ConfigPatchBodyItem{
										OP:    "replace",
										Path:  "/config/dynamic/authc/basic_internal_auth_domain/http_enabled",
										Value: true,
									},
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SecurityConfig.Patch(t.Context(), security.ConfigPatchReq{})
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
						testutil.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
