// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsPredictParams represents query parameters for ModelsPredictReq
type ModelsPredictParams struct{}

func (r ModelsPredictParams) get() map[string]string {
	return make(map[string]string)
}
