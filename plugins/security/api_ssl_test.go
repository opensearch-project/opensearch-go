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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecuritySSLClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	config, err := ossectest.ClientConfig(t)
	require.NoError(t, err)

	osAPIclient, err := testutil.NewClient(t)
	require.NoError(t, err)

	testutil.SkipIfBelowVersion(t, osAPIclient, 2, 0, "SSLClient")

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

	type sslTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []sslTests
	}{
		{
			Name: "HTTPReload",
			Tests: []sslTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.SSL.HTTPReload(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.HTTPReload(t.Context(), &security.SSLHTTPReloadReq{})
					},
				},
			},
		},
		{
			Name: "TransportReload",
			Tests: []sslTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.SSL.TransportReload(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.TransportReload(t.Context(), &security.SSLTransportReloadReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []sslTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.SSL.Get(t.Context(), nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.Get(t.Context(), nil)
					},
				},
			},
		},
	}
	for _, value := range testCases {
		t.Run(value.Name, func(t *testing.T) {
			for _, testCase := range value.Tests {
				t.Run(testCase.Name, func(t *testing.T) {
					if strings.HasSuffix(value.Name, "Reload") && strings.Contains(testCase.Name, "request") {
						testutil.SkipIfBelowVersion(t, osAPIclient, 2, 8, value.Name)
					}
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
