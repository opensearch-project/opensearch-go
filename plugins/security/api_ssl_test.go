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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSSLClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	config, err := ossectest.ClientConfig()
	require.Nil(t, err)

	osAPIclient, err := ostest.NewClient()
	require.Nil(t, err)

	ostest.SkipIfBelowVersion(t, osAPIclient, 1, 3, "SSLClient")

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
						return client.SSL.HTTPReload(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.HTTPReload(nil, &security.SSLHTTPReloadReq{})
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
						return client.SSL.TransportReload(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.TransportReload(nil, &security.SSLTransportReloadReq{})
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
						return client.SSL.Get(nil, nil)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.SSL.Get(nil, nil)
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
						ostest.SkipIfBelowVersion(t, osAPIclient, 2, 7, value.Name)
					}
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
