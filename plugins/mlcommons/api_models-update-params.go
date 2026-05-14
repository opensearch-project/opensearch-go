// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsUpdateParams represents query parameters for ModelsUpdateReq
type ModelsUpdateParams struct{}

func (r ModelsUpdateParams) get() map[string]string {
	return make(map[string]string)
}
