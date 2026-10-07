// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompress(t *testing.T) {
	t.Run("initialize & compress", func(t *testing.T) {
		gzipCompressor := newGzipRequestCompressor(t, gzip.DefaultCompression)
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// unzip
		r, _ := gzip.NewReader(buf)
		s, _ := io.ReadAll(r)
		if string(s) != body {
			t.Fatalf("expected body to be the same after compressing and decompressing: expected %s, got %s", body, string(s))
		}
	})

	t.Run("gzip multiple times", func(t *testing.T) {
		gzipCompressor := newGzipRequestCompressor(t, gzip.DefaultCompression)
		for range 5 {
			body := generateRandomString()
			rc := io.NopCloser(strings.NewReader(body))

			buf, err := gzipCompressor.compress(rc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// unzip
			r, _ := gzip.NewReader(buf)
			s, _ := io.ReadAll(r)
			if string(s) != body {
				t.Fatal("expected body to be the same after compressing and decompressing")
			}
		}
	})

	t.Run("ensure gzipped data is smaller and different from original", func(t *testing.T) {
		gzipCompressor := newGzipRequestCompressor(t, gzip.DefaultCompression)
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(buf.Bytes()) <= len(body) {
			t.Fatalf("expected compressed data to be smaller than original: expected %d, got %d", len(body), len(buf.Bytes()))
		}

		if body == buf.String() {
			t.Fatalf("expected compressed data to be different from original")
		}
	})

	t.Run("compressing data twice", func(t *testing.T) {
		gzipCompressor := newGzipRequestCompressor(t, gzip.DefaultCompression)
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		rc = io.NopCloser(buf)
		buf2, err := gzipCompressor.compress(rc)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// unzip
		r, _ := gzip.NewReader(buf2)
		r, _ = gzip.NewReader(r)
		s, _ := io.ReadAll(r)
		if string(s) != body {
			t.Fatal("expected body to be the same after compressing and decompressing twice")
		}
	})
}

func generateRandomString() string {
	length := rand.Intn(100) + 1

	// Define the characters that can be used in the random string
	charset := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

	// Create a byte slice with the specified length
	randomBytes := make([]byte, length)

	// Generate a random character from the charset for each byte in the slice
	for i := range length {
		randomBytes[i] = charset[rand.Intn(len(charset))]
	}

	// Convert the byte slice to a string and return it
	return string(randomBytes)
}

// gzipBytes returns s gzipped at the default level.
func gzipBytes(t *testing.T, s string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, err := zw.Write([]byte(s))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// gunzip inflates b exactly once, so a double-gzipped body comes back as gzip
// bytes rather than plaintext.
func gunzip(t *testing.T, b []byte, msgAndArgs ...any) string {
	t.Helper()

	zr, err := gzip.NewReader(bytes.NewReader(b))
	require.NoError(t, err, msgAndArgs...)
	plain, err := io.ReadAll(zr)
	require.NoError(t, err, msgAndArgs...)
	return string(plain)
}

// newGzipRequestCompressor returns the transport's compressor for GZip(level).
func newGzipRequestCompressor(t *testing.T, level int) *requestCompressor {
	t.Helper()

	c, err := GZip(level)
	require.NoError(t, err)
	rc, err := newRequestCompressor(c)
	require.NoError(t, err)
	return rc
}

// gzipRoundTrip compresses body with c, requires it to inflate back to body,
// and returns the compressed length.
func gzipRoundTrip(t *testing.T, c Compressor, body string) int {
	t.Helper()

	rc, err := newRequestCompressor(c)
	require.NoError(t, err)
	buf, err := rc.compress(strings.NewReader(body))
	require.NoError(t, err)

	require.Equal(t, body, gunzip(t, buf.Bytes()))
	return buf.Len()
}

func TestGZipLevel(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("opensearch ", 1000)

	tests := []struct {
		name    string
		level   int
		wantErr bool
	}{
		{name: "default", level: gzip.DefaultCompression},
		{name: "no compression", level: gzip.NoCompression},
		{name: "best speed", level: gzip.BestSpeed},
		{name: "best compression", level: gzip.BestCompression},
		{name: "huffman only", level: gzip.HuffmanOnly},
		{name: "below range", level: gzip.HuffmanOnly - 1, wantErr: true},
		{name: "above range", level: gzip.BestCompression + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := GZip(tt.level)
			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, c)
				return
			}
			require.NoError(t, err)
			require.Equal(t, encodingGzip, c.ContentEncoding())
			gzipRoundTrip(t, c, body)
		})
	}
}

// TestGZipLevelReachesWriter pins that the level configures the pooled writers:
// stored (level 0) output of a repetitive body is larger than best-compression
// output.
func TestGZipLevelReachesWriter(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("opensearch ", 1000)

	stored, err := GZip(gzip.NoCompression)
	require.NoError(t, err)
	best, err := GZip(gzip.BestCompression)
	require.NoError(t, err)

	require.Greater(t, gzipRoundTrip(t, stored, body), gzipRoundTrip(t, best, body))
}
