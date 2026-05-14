// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsDeleteParams represents query parameters for ModelsDeleteReq
type ModelsDeleteParams struct{}

func (r ModelsDeleteParams) get() map[string]string {
	return make(map[string]string)
}
