// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
)

// Explain executes a explain policy request with the optional ExplainReq
func (c Client) Explain(ctx context.Context, req *ExplainReq) (ExplainResp, *opensearch.Response, error) {
	if req == nil {
		req = &ExplainReq{}
	}

	var data ExplainResp

	resp, err := c.do(ctx, req, &data)
	if err != nil {
		return data, resp, err
	}

	return data, resp, nil
}

// ExplainReq represents possible options for the explain policy request
type ExplainReq struct {
	Indices []string

	Params ExplainParams
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ExplainReq) GetRequest() (*http.Request, error) {
	indices := strings.Join(r.Indices, ",")
	var path strings.Builder
	path.Grow(len("/_plugins/_ism/explain/") + len(indices))
	path.WriteString("/_plugins/_ism/explain")
	if len(r.Indices) > 0 {
		path.WriteString("/")
		path.WriteString(indices)
	}

	return opensearch.BuildRequest(
		http.MethodGet,
		path.String(),
		nil,
		r.Params.get(),
		r.Header,
	)
}

// ExplainResp represents the returned struct of the explain policy response
type ExplainResp struct {
	Indices             map[string]ExplainItem
	TotalManagedIndices int `json:"total_managed_indices"`
}

// ExplainItem is a sub type of ExplainResp containing information about the policy attached to the index
type ExplainItem struct {
	PluginPolicyID      *string `json:"index.plugins.index_state_management.policy_id"`
	OpenDistroPolicyID  *string `json:"index.opendistro.index_state_management.policy_id"`
	Index               string  `json:"index,omitempty"`
	IndexUUID           string  `json:"index_uuid,omitempty"`
	PolicyID            string  `json:"policy_id,omitempty"`
	PolicySeqNo         int     `json:"policy_seq_no,omitempty"`
	PolicyPrimaryTerm   int     `json:"policy_primary_term,omitempty"`
	RolledOver          bool    `json:"rolled_over,omitempty"`
	RolledOverIndexName string  `json:"rolled_over_index_name,omitempty"`
	IndexCreationDate   int64   `json:"index_creation_date,omitempty"`
	State               *struct {
		Name      string `json:"name"`
		StartTime int64  `json:"start_time"`
	} `json:"state,omitempty"`
	Action *struct {
		Name            string `json:"name"`
		StartTime       int64  `json:"start_time"`
		Index           int    `json:"index"`
		Failed          bool   `json:"failed"`
		ConsumedRetries int    `json:"consumed_retries"`
		LastRetryTime   int64  `json:"last_retry_time"`
	} `json:"action,omitempty"`
	Step *struct {
		Name       string `json:"name"`
		StartTime  int64  `json:"start_time"`
		StepStatus string `json:"step_status"`
	} `json:"step,omitempty"`
	RetryInfo *struct {
		Failed          bool `json:"failed"`
		ConsumedRetries int  `json:"consumed_retries"`
	} `json:"retry_info,omitempty"`
	Info *struct {
		Message string `json:"message"`
	} `json:"info,omitempty"`
	Enabled  *bool       `json:"enabled"`
	Policy   *PolicyBody `json:"policy,omitempty"`
	Validate *struct {
		Message string `json:"validation_message"`
		Status  string `json:"validation_status"`
	} `json:"validate,omitempty"`
}

// UnmarshalJSON is a custom unmarshal function for ExplainResp as the default Unmarshal can not handle it correctly
func (r *ExplainResp) UnmarshalJSON(b []byte) error {
	var dummy struct {
		index map[string]json.RawMessage
	}
	if err := json.Unmarshal(b, &dummy.index); err != nil {
		return err
	}
	if r.Indices == nil {
		r.Indices = make(map[string]ExplainItem)
	}
	for key, value := range dummy.index {
		if key == "total_managed_indices" {
			var intDummy int
			if err := json.Unmarshal(value, &intDummy); err != nil {
				return err
			}
			r.TotalManagedIndices = intDummy
			continue
		}
		var itemDummy ExplainItem
		if err := json.Unmarshal(value, &itemDummy); err != nil {
			return err
		}
		r.Indices[key] = itemDummy
	}
	return nil
}

// MarshalJSON is a custom marshal function for ExplainResp as the default Unmarshal can not handle it correctly
func (r *ExplainResp) MarshalJSON() ([]byte, error) {
	var dummy struct {
		index map[string]any
	}
	dummy.index = make(map[string]any)
	for key, value := range r.Indices {
		dummy.index[key] = value
	}
	dummy.index["total_managed_indices"] = r.TotalManagedIndices
	return json.Marshal(dummy.index)
}
