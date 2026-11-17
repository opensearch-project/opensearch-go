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
	"github.com/opensearch-project/opensearch-go/v5/plugins/search_relevance"
)

// The server picks the experiment kind from "type", so each constructor sets it
// and a body decodes to the branch its type names. The experiment branches
// accept the same fields, so decoding by shape would pick the first branch
// rather than the one the type names.
func TestPutExperimentsBodyDiscriminator(t *testing.T) {
	t.Parallel()

	base := opensearchapi.SearchRelevancePutExperimentRequestBase{
		QuerySetID:              "q",
		SearchConfigurationList: []string{"a"},
		Size:                    10,
	}
	tests := []struct {
		name     string
		body     search_relevance.SearchRelevancePutExperimentsBody
		wantWire string
		wantType search_relevance.SearchRelevancePutExperimentsBodyType
	}{
		{
			name: "pairwise",
			body: search_relevance.NewSearchRelevancePutExperimentsBodyFromPutPairwiseExperimentRequest(
				opensearchapi.SearchRelevancePutPairwiseExperimentRequest{SearchRelevancePutExperimentRequestBase: base},
			),
			wantWire: `{"querySetId":"q","searchConfigurationList":["a"],"size":10,"type":"PAIRWISE_COMPARISON"}`,
			wantType: search_relevance.SearchRelevancePutExperimentsBodyPutPairwiseExperimentRequestType,
		},
		{
			name: "pointwise",
			body: search_relevance.NewSearchRelevancePutExperimentsBodyFromPutPointwiseExperimentRequest(
				opensearchapi.SearchRelevancePutPointwiseExperimentRequest{SearchRelevancePutExperimentRequestBase: base},
			),
			wantWire: `{"querySetId":"q","searchConfigurationList":["a"],"size":10,"type":"POINTWISE_EVALUATION"}`,
			wantType: search_relevance.SearchRelevancePutExperimentsBodyPutPointwiseExperimentRequestType,
		},
		{
			name: "hybrid optimizer",
			body: search_relevance.NewSearchRelevancePutExperimentsBodyFromPutHybridOptimizerExperimentRequest(
				opensearchapi.SearchRelevancePutHybridOptimizerExperimentRequest{SearchRelevancePutExperimentRequestBase: base},
			),
			wantWire: `{"querySetId":"q","searchConfigurationList":["a"],"size":10,"type":"HYBRID_OPTIMIZER"}`,
			wantType: search_relevance.SearchRelevancePutExperimentsBodyPutHybridOptimizerExperimentRequestType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire, err := json.Marshal(tt.body)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantWire, string(wire))
			var back search_relevance.SearchRelevancePutExperimentsBody
			require.NoError(t, json.Unmarshal(wire, &back))
			require.Equal(t, tt.wantType, back.Type())
		})
	}
}

// The server picks the judgment kind from "type" in the same way. Each judgment
// branch requires its own fields, but a body that leaves them out would still
// decode by shape as the first branch, so the decoder goes by "type".
func TestPutJudgmentsBodyDiscriminator(t *testing.T) {
	t.Parallel()

	base := opensearchapi.SearchRelevancePutJudgmentsRequestBase{Name: "n"}
	tests := []struct {
		name     string
		body     search_relevance.SearchRelevancePutJudgmentsBody
		wantWire string
		wantType search_relevance.SearchRelevancePutJudgmentsBodyType
	}{
		{
			name: "llm",
			body: search_relevance.NewSearchRelevancePutJudgmentsBodyFromPutLLMJudgmentsRequest(
				opensearchapi.SearchRelevancePutLLMJudgmentsRequest{
					SearchRelevancePutJudgmentsRequestBase: base,
					ModelID:                                "m",
					QuerySetID:                             "q",
				},
			),
			wantWire: `{"name":"n","modelId":"m","querySetId":"q","type":"LLM_JUDGMENT"}`,
			wantType: search_relevance.SearchRelevancePutJudgmentsBodyPutLLMJudgmentsRequestType,
		},
		{
			name: "ubi",
			body: search_relevance.NewSearchRelevancePutJudgmentsBodyFromPutUBIJudgmentsRequest(
				opensearchapi.SearchRelevancePutUBIJudgmentsRequest{
					SearchRelevancePutJudgmentsRequestBase: base,
					ClickModel:                             "coec",
					MaxRank:                                20,
				},
			),
			wantWire: `{"name":"n","clickModel":"coec","maxRank":20,"type":"UBI_JUDGMENT"}`,
			wantType: search_relevance.SearchRelevancePutJudgmentsBodyPutUBIJudgmentsRequestType,
		},
		{
			name: "import",
			body: search_relevance.NewSearchRelevancePutJudgmentsBodyFromPutImportJudgmentsRequest(
				opensearchapi.SearchRelevancePutImportJudgmentsRequest{
					SearchRelevancePutJudgmentsRequestBase: base,
					JudgmentRatings:                        []json.RawMessage{},
				},
			),
			wantWire: `{"name":"n","judgmentRatings":[],"type":"IMPORT_JUDGMENT"}`,
			wantType: search_relevance.SearchRelevancePutJudgmentsBodyPutImportJudgmentsRequestType,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wire, err := json.Marshal(tt.body)
			require.NoError(t, err)
			require.JSONEq(t, tt.wantWire, string(wire))
			var back search_relevance.SearchRelevancePutJudgmentsBody
			require.NoError(t, json.Unmarshal(wire, &back))
			require.Equal(t, tt.wantType, back.Type())
		})
	}
}
