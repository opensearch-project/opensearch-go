// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (plugins || plugin_security)

package security_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/plugins/security"
	ossectest "github.com/opensearch-project/opensearch-go/v4/plugins/security/internal/test"
)

func TestSecurityAuditClient(t *testing.T) {
	testutil.SkipIfNotSecure(t)

	// The security plugin has a race condition during multi-node cluster init:
	// opensearch-onetime-setup.sh runs on all nodes, but only one wins the
	// .opendistro_security index initialization. ConfigurationLoaderSecurity7
	// can reload before the audit config doc is fully replicated, leaving
	// isAuditConfigDocPresentInIndex=false. When that happens, the Audit API
	// rejects all requests with "Method X not supported for this action."
	//
	// The flag is not re-evaluated on a timer -- only on config reload events.
	// Flushing the security cache (DELETE /_plugins/_security/api/cache) fires
	// a ConfigUpdateAction with all CType values, forcing cl.load() to re-read
	// the audit doc from the index. If the doc was already replicated (it was --
	// it ships in every Docker image since 1.0), the flag flips to true.
	client, err := ossectest.NewClient(t)
	require.NoError(t, err)

	if _, getErr := client.Audit.Get(t.Context(), nil); getErr != nil {
		if strings.Contains(getErr.Error(), "not supported for this action") {
			// Force a config reload by flushing the security cache.
			_, flushErr := client.FlushCache(t.Context(), nil)
			require.NoError(t, flushErr, "failed to flush security cache")

			// After the flush, the config reload is synchronous on the node
			// that handled the request. Give a brief window for the reload
			// to propagate, then retry.
			require.Eventually(t, func() bool {
				_, retryErr := client.Audit.Get(t.Context(), nil)
				return retryErr == nil
			}, 10*time.Second, 500*time.Millisecond,
				"audit config still unavailable after cache flush -- security plugin init race")
		} else {
			require.NoError(t, getErr)
		}
	}

	failingClient, err := ossectest.CreateFailingClient(t)
	require.NoError(t, err)

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
						return client.Audit.Get(t.Context(), nil)
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
						// Re-fetch the audit config to get current state.
						// The config may have been modified by concurrent tests
						// or background operations since the initial Get.
						getResp, err := client.Audit.Get(t.Context(), nil)
						if err != nil {
							return getResp, err
						}
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
