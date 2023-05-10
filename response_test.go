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

package opensearch_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v2"
)

func TestResponse(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		resp := opensearch.Response{}
		assert.Equal(t, "[0 <nil>]", resp.Status())
		assert.Equal(t, "[0 <nil>]", resp.String())
	})

	t.Run("with StatusCode", func(t *testing.T) {
		resp := opensearch.Response{StatusCode: http.StatusOK}
		assert.Equal(t, "[200 OK]", resp.Status())
		assert.Equal(t, "[200 OK]", resp.String())
	})

	t.Run("with StatusCode and Body", func(t *testing.T) {
		resp := opensearch.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{\"test\": true}"))}
		assert.Equal(t, "[200 OK]", resp.Status())
		assert.Equal(t, "[200 OK] {\"test\": true}", resp.String())
	})

	t.Run("with StatusCode and failing Body", func(t *testing.T) {
		resp := opensearch.Response{StatusCode: http.StatusOK, Body: io.NopCloser(iotest.ErrReader(errors.New("io reader test")))}
		assert.Equal(t, "[200 OK]", resp.Status())
		assert.Equal(t, "[200 OK] <error reading response body: io reader test>", resp.String())
	})
}
