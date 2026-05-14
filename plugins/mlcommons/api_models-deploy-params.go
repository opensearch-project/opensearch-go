// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsDeployParams represents query parameters for ModelsDeployReq
type ModelsDeployParams struct{}

func (r ModelsDeployParams) get() map[string]string {
	return make(map[string]string)
}
