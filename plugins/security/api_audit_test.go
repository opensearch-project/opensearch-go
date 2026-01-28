// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityAuditClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)

	osAPIclient, err := testutil.NewClient(t)
	require.NoError(t, err)

	testutil.SkipIfBelowVersion(t, osAPIclient, 2, 15, "Audit API")

	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	var getResp security.AuditGetResp

	type auditTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []auditTests
	}{
		{
			Name: "Get",
			Tests: []auditTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						getResp, err := client.Audit.Get(t.Context(), nil)
						return getResp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Audit.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Put",
			Tests: []auditTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Audit.Put(
							t.Context(),
							security.AuditPutReq{
								Body: security.AuditPutBody{
									Compliance: getResp.Config.Compliance,
									Enabled:    getResp.Config.Enabled,
									Audit:      getResp.Config.Audit,
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Audit.Put(t.Context(), security.AuditPutReq{})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []auditTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Audit.Patch(
							t.Context(),
							security.AuditPatchReq{
								Body: security.AuditPatchBody{
									security.AuditPatchBodyItem{
										OP:    "add",
										Path:  "/config/enabled",
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
						return failingClient.Audit.Patch(t.Context(), security.AuditPatchReq{})
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
