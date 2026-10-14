// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package compress

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// TestNewEncoderErrorReturnsNilEncoder verifies that a writer constructor
// failure yields an untyped nil Encoder, so a caller's nil check holds. The
// exported constructors reject these levels first, so the compressors are built
// directly.
func TestNewEncoderErrorReturnsNilEncoder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    leveledCompressor
	}{
		{name: "gzip", c: leveledCompressor{encoding: encodingGzip, level: maxLevel + 1, newWriter: newGzipWriter}},
		{name: "deflate", c: leveledCompressor{encoding: encodingDeflate, level: maxLevel + 1, newWriter: newZlibWriter}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			enc, err := tt.c.NewEncoder(io.Discard)
			require.Error(t, err)
			// require.Nil also accepts a typed nil pointer; Equal against an
			// untyped nil does not.
			require.Equal(t, opensearchtransport.Encoder(nil), enc)
		})
	}
}
