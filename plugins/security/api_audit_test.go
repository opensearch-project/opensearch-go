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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestAuditClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

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
						getResp, err := client.Audit.Get(nil, nil)
						return getResp, err
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Audit.Get(nil, nil)
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
							nil,
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
						return failingClient.Audit.Put(nil, security.AuditPutReq{})
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
							nil,
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
						return failingClient.Audit.Patch(nil, security.AuditPatchReq{})
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
