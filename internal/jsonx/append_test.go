// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package jsonx_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// TestAppendString checks each string AppendString writes against what
// encoding/json writes for it, with and without HTML escaping, and that it
// appends nothing for a string the encoder would escape.
func TestAppendString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		s      string
		wantOK map[bool]bool // by escapeHTML
	}{
		{name: "empty", s: "", wantOK: map[bool]bool{true: true, false: true}},
		{name: "printable ASCII", s: "doc-1 ~!#$%'()*+,./:;=?@[]^_`{|}", wantOK: map[bool]bool{true: true, false: true}},
		{name: "HTML characters", s: "a<b>&c", wantOK: map[bool]bool{false: true}},
		{name: "a quote", s: `a"b`},
		{name: "a backslash", s: `a\b`},
		{name: "a control byte", s: "a\tb"},
		{name: "DEL", s: "a\x7fb"},
		{name: "non-ASCII", s: "é"},
		{name: "invalid UTF-8", s: "\xff"},
	}
	for _, tt := range tests {
		for _, escapeHTML := range []bool{true, false} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, ok := jsonx.AppendString([]byte("prior,"), tt.s, escapeHTML)
				require.Equal(t, tt.wantOK[escapeHTML], ok, "escapeHTML=%v", escapeHTML)
				if !ok {
					require.Equal(t, "prior,", string(got), "appends nothing when it leaves the string to the encoder")
					return
				}

				var want bytes.Buffer
				enc := json.NewEncoder(&want)
				enc.SetEscapeHTML(escapeHTML)
				require.NoError(t, enc.Encode(tt.s))
				require.Equal(t, "prior,"+string(bytes.TrimSuffix(want.Bytes(), []byte("\n"))), string(got))
			})
		}
	}
}
