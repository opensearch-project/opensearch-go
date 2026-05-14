// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// ModelsUploadChunkParams represents query parameters for ModelsUploadChunkReq
type ModelsUploadChunkParams struct{}

func (r ModelsUploadChunkParams) get() map[string]string {
	return make(map[string]string)
}
