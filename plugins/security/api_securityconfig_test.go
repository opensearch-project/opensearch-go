// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration

package security_test

import (
	"crypto/tls"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v3/internal/test"
	"github.com/opensearch-project/opensearch-go/v3/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v3/plugins/security/internal/test"
)

func TestSecurityConfigClient(t *testing.T) {
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

	var putBody security.ConfigDynamic

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
						resp, err := client.SecurityConfig.Get(nil, nil)
						putBody = resp.Config.Dynamic
						return resp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SecurityConfig.Get(nil, nil)
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
						return client.SecurityConfig.Put(
							nil,
							security.ConfigPutReq{
								Body: security.ConfigPutBody{Dynamic: putBody},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SecurityConfig.Put(nil, security.ConfigPutReq{})
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
							nil,
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
						return failingClient.SecurityConfig.Patch(nil, security.ConfigPatchReq{})
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
						ossectest.VerifyInspect(t, res.Inspect())
					} else {
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
					}
				})
			}
		})
	}
}
