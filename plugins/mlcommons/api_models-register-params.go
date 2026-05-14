// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsRegisterParams represents query parameters for ModelsRegisterReq
type ModelsRegisterParams struct {
	// Deploy registers and deploys the model in a single call. Equivalent to ModelsRegisterBody.Deploy.
	Deploy *bool
}

func (r ModelsRegisterParams) get() map[string]string {
	params := make(map[string]string)

	if r.Deploy != nil {
		if *r.Deploy {
			params["deploy"] = "true"
		} else {
			params["deploy"] = "false"
		}
	}

	return params
}
