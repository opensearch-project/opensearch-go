// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mlcommons

// TasksGetParams represents query parameters for TasksGetReq
type TasksGetParams struct{}

func (r TasksGetParams) get() map[string]string {
	return make(map[string]string)
}
