// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package search_relevance_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
	sr "github.com/opensearch-project/opensearch-go/v5/plugins/search_relevance"
)

// The server picks the experiment kind from "type", so each constructor sets it
// and a body decodes to the branch its type names. Each branch's body here is
// also valid for the other branches, so decoding by shape would pick the first
// branch rather than the one the type names.
func TestPutExperimentsBodyDiscriminator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     sr.SearchRelevancePutExperimentsBody
		wantWire string
		wantType sr.SearchRelevancePutExperimentsBodyType
	}{
		{
			name: "pairwise",
			body: sr.NewSearchRelevancePutExperimentsBodyFromPutPairwiseExperimentRequest(
				opensearchapi.SearchRelevancePutPairwiseExperimentRequest{},
			),
			wantWire: `{"type":"PAIRWISE_COMPARISON"}`,
			wantType: sr.SearchRelevancePutExperimentsBodyPutPairwiseExperimentRequestType,
		},
		{
			name: "pointwise",
			body: sr.NewSearchRelevancePutExperimentsBodyFromPutPointwiseExperimentRequest(
				opensearchapi.SearchRelevancePutPointwiseExperimentRequest{},
			),
			wantWire: `{"type":"POINTWISE_EVALUATION"}`,
			wantType: sr.SearchRelevancePutExperimentsBodyPutPointwiseExperimentRequestType,
		},
		{
			name: "hybrid optimizer",
			body: sr.NewSearchRelevancePutExperimentsBodyFromPutHybridOptimizerExperimentRequest(
				opensearchapi.SearchRelevancePutHybridOptimizerExperimentRequest{},
			),
			wantWire: `{"type":"HYBRID_OPTIMIZER"}`,
			wantType: sr.SearchRelevancePutExperimentsBodyPutHybridOptimizerExperimentRequestType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire, err := json.Marshal(tt.body)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantWire, string(wire))
			var back sr.SearchRelevancePutExperimentsBody
			require.NoError(t, json.Unmarshal(wire, &back))
			require.Equal(t, tt.wantType, back.Type())
		})
	}
}

// The server picks the judgment kind from "type" in the same way, and each
// branch's body here is again valid for the other branches.
func TestPutJudgmentsBodyDiscriminator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     sr.SearchRelevancePutJudgmentsBody
		wantWire string
		wantType sr.SearchRelevancePutJudgmentsBodyType
	}{
		{
			name:     "llm",
			body:     sr.NewSearchRelevancePutJudgmentsBodyFromPutLLMJudgmentsRequest(opensearchapi.SearchRelevancePutLLMJudgmentsRequest{}),
			wantWire: `{"type":"LLM_JUDGMENT"}`,
			wantType: sr.SearchRelevancePutJudgmentsBodyPutLLMJudgmentsRequestType,
		},
		{
			name:     "ubi",
			body:     sr.NewSearchRelevancePutJudgmentsBodyFromPutUBIJudgmentsRequest(opensearchapi.SearchRelevancePutUBIJudgmentsRequest{}),
			wantWire: `{"type":"UBI_JUDGMENT"}`,
			wantType: sr.SearchRelevancePutJudgmentsBodyPutUBIJudgmentsRequestType,
		},
		{
			name:     "import",
			body:     sr.NewSearchRelevancePutJudgmentsBodyFromPutImportJudgmentsRequest(opensearchapi.SearchRelevancePutImportJudgmentsRequest{}),
			wantWire: `{"type":"IMPORT_JUDGMENT"}`,
			wantType: sr.SearchRelevancePutJudgmentsBodyPutImportJudgmentsRequestType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire, err := json.Marshal(tt.body)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantWire, string(wire))
			var back sr.SearchRelevancePutJudgmentsBody
			require.NoError(t, json.Unmarshal(wire, &back))
			require.Equal(t, tt.wantType, back.Type())
		})
	}
}
