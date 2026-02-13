// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityTenantsClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.NoError(t, err)

	testTenant := "test_tenant"

	type tenantsTests struct {
		Name    string
		Results func() (ossectest.Response, error)
	}

	testCases := []struct {
		Name  string
		Tests []tenantsTests
	}{
		{
			Name: "Put",
			Tests: []tenantsTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Put(
							t.Context(),
							security.TenantsPutReq{
								Tenant: testTenant,
								Body: security.TenantsPutBody{
									Description: "Test",
								},
							},
						)
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Tenants.Put(t.Context(), security.TenantsPutReq{})
					},
				},
			},
		},
		{
			Name: "Get",
			Tests: []tenantsTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Get(t.Context(), nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Get(t.Context(), &security.TenantsGetReq{Tenant: testTenant})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Tenants.Get(t.Context(), nil)
					},
				},
			},
		},
		{
			Name: "Delete",
			Tests: []tenantsTests{
				{
					Name: "without request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Delete(t.Context(), security.TenantsDeleteReq{Tenant: testTenant})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Tenants.Delete(t.Context(), security.TenantsDeleteReq{Tenant: testTenant})
					},
				},
			},
		},
		{
			Name: "Patch",
			Tests: []tenantsTests{
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Patch(
							t.Context(),
							security.TenantsPatchReq{
								Body: security.TenantsPatchBody{
									security.TenantsPatchBodyItem{
										OP:   "add",
										Path: "/test",
										Value: security.TenantsPutBody{
											Description: "Test",
										},
									},
									security.TenantsPatchBodyItem{
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
						return failingClient.Tenants.Patch(t.Context(), security.TenantsPatchReq{})
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
						if err != nil {
							fmt.Println(err)
						}
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
			resp, err := client.Tenants.Get(t.Context(), nil)
			require.NoError(t, err)
			assert.NotNil(t, resp)
			testutil.CompareRawJSONwithParsedJSON(t, resp.Tenants, resp.Inspect().Response)
		})
	})
}
