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
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
)

// String uses a value receiver and is non-consuming, so both Response and
// *Response satisfy fmt.Stringer. These guards pin that contract so a revert to
// a pointer-only receiver fails the build.
var (
	_ fmt.Stringer = opensearch.Response{}
	_ fmt.Stringer = (*opensearch.Response)(nil)
)

func TestResponseValueIsStringer(t *testing.T) {
	var v any = opensearch.Response{}
	_, ok := v.(fmt.Stringer)
	require.True(t, ok, "Response value must satisfy fmt.Stringer; String() is a value receiver")
}

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

	t.Run("repeated String is consistent for a live body", func(t *testing.T) {
		resp := opensearch.NewResponse(http.StatusOK, io.NopCloser(strings.NewReader(`{"test": true}`)), nil)

		// The first call reads Body to render it; the shared render cache makes
		// every later call return the same text without re-reading, even though
		// the underlying Body reader has now been consumed. (A value receiver
		// cannot write Body back to the caller, so for a hand-built Response
		// with only a live Body the reader itself is spent after the first
		// render; the rawBody path produced by Client.Do stays fully
		// non-consuming -- see the internal TestResponseString_RawBody test.)
		require.Equal(t, `[200 OK] {"test": true}`, resp.String())
		require.Equal(t, `[200 OK] {"test": true}`, resp.String())
	})
}
