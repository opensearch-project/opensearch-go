// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package osapi

import "encoding/json"

// TotalHits represents the total number of search hits. The server returns
// this as either an object {"value": N, "relation": "eq"|"gte"} or a bare
// integer (when rest_total_hits_as_int=true). This type handles both forms.
type TotalHits struct {
	Value    int64  `json:"value"`
	Relation string `json:"relation"`
}

// UnmarshalJSON handles both integer and object forms of total hits.
func (t *TotalHits) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	// Try integer form first (bare number).
	if data[0] >= '0' && data[0] <= '9' {
		return json.Unmarshal(data, &t.Value)
	}

	// Object form.
	type alias TotalHits
	return json.Unmarshal(data, (*alias)(t))
}
