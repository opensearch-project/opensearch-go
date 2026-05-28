// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil

// NodesMeta contains transport-level metadata returned by node-aware APIs.
type NodesMeta struct {
	Total      int             `json:"total"`
	Successful int             `json:"successful"`
	Failed     int             `json:"failed"`
	Failures   []FailuresCause `json:"failures"`
}

// FailuresCause contains information about a node failure.
type FailuresCause struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
	NodeID string `json:"node_id"`
	Cause  *struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
		Cause  *struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
			Cause  *struct {
				Type   string  `json:"type"`
				Reason *string `json:"reason"`
			} `json:"caused_by,omitempty"`
		} `json:"caused_by,omitempty"`
	} `json:"caused_by,omitempty"`
}
