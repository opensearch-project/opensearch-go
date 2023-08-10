// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package opensearchapi

import (
	"strconv"
)

// ClusterAllocationExplainParams represents possible parameters for the ClusterAllocationExplainReq
type ClusterAllocationExplainParams struct {
	IncludeDiskInfo     *bool
	IncludeYesDecisions *bool

	Pretty     bool
	Human      bool
	ErrorTrace bool
}

func (r ClusterAllocationExplainParams) get() map[string]string {
	params := make(map[string]string)

	if r.IncludeDiskInfo != nil {
		params["include_disk_info"] = strconv.FormatBool(*r.IncludeDiskInfo)
	}

	if r.IncludeYesDecisions != nil {
		params["include_yes_decisions"] = strconv.FormatBool(*r.IncludeYesDecisions)
	}

	if r.Pretty {
		params["pretty"] = "true"
	}

	if r.Human {
		params["human"] = "true"
	}

	if r.ErrorTrace {
		params["error_trace"] = "true"
	}

	return params
}
