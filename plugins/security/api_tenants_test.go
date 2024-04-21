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

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestTenantsClient(t *testing.T) {
	ostest.SkipIfNotSecure(t)
	client, err := ossectest.NewClient()
	require.Nil(t, err)

	failingClient, err := ossectest.CreateFailingClient()
	require.Nil(t, err)

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
							nil,
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
						return failingClient.Tenants.Put(nil, security.TenantsPutReq{})
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
						return client.Tenants.Get(nil, nil)
					},
				},
				{
					Name: "with request",
					Results: func() (ossectest.Response, error) {
						return client.Tenants.Get(nil, &security.TenantsGetReq{Tenant: testTenant})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Tenants.Get(nil, nil)
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
						return client.Tenants.Delete(nil, security.TenantsDeleteReq{Tenant: testTenant})
					},
				},
				{
					Name: "inspect",
					Results: func() (ossectest.Response, error) {
						return failingClient.Tenants.Delete(nil, security.TenantsDeleteReq{Tenant: testTenant})
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
							nil,
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
						return failingClient.Tenants.Patch(nil, security.TenantsPatchReq{})
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
						if err != nil {
							fmt.Println(err)
						}
						require.Nil(t, err)
						require.NotNil(t, res)
						assert.NotNil(t, res.Inspect().Response)
						if value.Name != "Get" {
							ostest.CompareRawJSONwithParsedJSON(t, res, res.Inspect().Response)
						}
					}
				})
			}
		})
	}
	t.Run("ValidateResponse", func(t *testing.T) {
		t.Run("Get", func(t *testing.T) {
			resp, err := client.Tenants.Get(nil, nil)
			assert.Nil(t, err)
			assert.NotNil(t, resp)
			ostest.CompareRawJSONwithParsedJSON(t, resp.Tenants, resp.Inspect().Response)
		})
	})
}
