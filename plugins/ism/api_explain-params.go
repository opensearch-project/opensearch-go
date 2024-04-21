// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

// ExplainParams represents possible parameters for the ExplainReq
type ExplainParams struct {
	ShowPolicy     bool
	ValidateAction bool
}

func (r ExplainParams) get() map[string]string {
	params := make(map[string]string)

	if r.ShowPolicy {
		params["show_policy"] = "true"
	}

	if r.ValidateAction {
		params["validate_action"] = "true"
	}

	return params
}
