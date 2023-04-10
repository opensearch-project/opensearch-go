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
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchapi

import "fmt"

// Error represents the API error response.
type Error struct {
	Err    Err `json:"error"`
	Status int `json:"status"`
}

// Err represents the error of an API error response
type Err struct {
	RootCause []RootCause `json:"root_cause"`
	Type      string      `json:"type"`
	Reason    string      `json:"reason"`
	Index     string      `json:"index,omitempty"`
	IndexUUID string      `json:"index_uudi,omitempty"`
}

// RootCause represents the root_cause of an API error response
type RootCause struct {
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Index     string `json:"index,omitempty"`
	IndexUUID string `json:"index_uudi,omitempty"`
}

// Error returns a string.
func (e *Error) Error() string {
	return fmt.Sprintf("status: %d, type: %s, reason: %s, root_cause: %s", e.Status, e.Err.Type, e.Err.Reason, e.Err.RootCause)
}
