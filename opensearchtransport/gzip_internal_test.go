// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"compress/gzip"
	"io"
	"math/rand"
	"strings"
	"testing"
)

func TestCompress(t *testing.T) {
	t.Run("initialize & compress", func(t *testing.T) {
		gzipCompressor := newGzipCompressor()
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		defer gzipCompressor.collectBuffer(buf)
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
		gzipCompressor := newGzipCompressor()
		for i := 0; i < 5; i++ {
			body := generateRandomString()
			rc := io.NopCloser(strings.NewReader(body))

			buf, err := gzipCompressor.compress(rc)
			defer gzipCompressor.collectBuffer(buf)
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
		gzipCompressor := newGzipCompressor()
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		defer gzipCompressor.collectBuffer(buf)
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
		gzipCompressor := newGzipCompressor()
		body := generateRandomString()
		rc := io.NopCloser(strings.NewReader(body))

		buf, err := gzipCompressor.compress(rc)
		defer gzipCompressor.collectBuffer(buf)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		rc = io.NopCloser(buf)
		buf2, err := gzipCompressor.compress(rc)
		defer gzipCompressor.collectBuffer(buf2)
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
	for i := 0; i < length; i++ {
		randomBytes[i] = charset[rand.Intn(len(charset))]
	}

	// Convert the byte slice to a string and return it
	return string(randomBytes)
}
