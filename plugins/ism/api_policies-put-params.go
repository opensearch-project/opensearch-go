// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ism

import "strconv"

// PoliciesPutParams represents possible parameters for the PoliciesPutReq
type PoliciesPutParams struct {
	IfSeqNo       *int
	IfPrimaryTerm *int
}

func (r PoliciesPutParams) get() map[string]string {
	params := make(map[string]string)

	if r.IfSeqNo != nil {
		params["if_seq_no"] = strconv.FormatInt(int64(*r.IfSeqNo), 10)
	}

	if r.IfPrimaryTerm != nil {
		params["if_primary_term"] = strconv.FormatInt(int64(*r.IfPrimaryTerm), 10)
	}

	return params
}
