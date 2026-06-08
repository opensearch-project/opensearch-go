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

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

func TestResponse(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		resp := opensearch.NewResponse(0, nil, nil)
		require.Equal(t, "[0 <nil>]", resp.Status())
		require.Equal(t, "[0 <nil>]", resp.String())
	})

	t.Run("with StatusCode", func(t *testing.T) {
		resp := opensearch.NewResponse(http.StatusOK, nil, nil)
		require.Equal(t, "[200 OK]", resp.Status())
		require.Equal(t, "[200 OK]", resp.String())
	})

	t.Run("with StatusCode and Body", func(t *testing.T) {
		resp := opensearch.NewResponse(http.StatusOK, io.NopCloser(strings.NewReader("{\"test\": true}")), nil)
		require.Equal(t, "[200 OK]", resp.Status())
		require.Equal(t, "[200 OK] {\"test\": true}", resp.String())
	})

	t.Run("with StatusCode and failing Body", func(t *testing.T) {
		resp := opensearch.NewResponse(http.StatusOK, io.NopCloser(iotest.ErrReader(errors.New("io reader test"))), nil)
		require.Equal(t, "[200 OK]", resp.Status())
		require.Equal(t, "[200 OK] <error reading response body: io reader test>", resp.String())
	})

	t.Run("String is non-consuming", func(t *testing.T) {
		resp := opensearch.NewResponse(http.StatusOK, io.NopCloser(strings.NewReader(`{"test": true}`)), nil)

		// First call renders the body; it must restore Body so a second
		// call (and any later body read) still sees the full payload.
		require.Equal(t, "[200 OK] {\"test\": true}", resp.String())
		require.Equal(t, "[200 OK] {\"test\": true}", resp.String())

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, `{"test": true}`, string(body))
	})
}
