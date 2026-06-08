// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.
//
//go:build !integration

package aws_test

import (
	"bytes"
	"errors"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/require"

	osaws "github.com/opensearch-project/opensearch-go/v4/signer/aws"
)

func TestConstants(t *testing.T) {
	require.Equal(t, "es", osaws.OpenSearchService)
	require.Equal(t, "aoss", osaws.OpenSearchServerless)
}

func TestV4Signer(t *testing.T) {
	t.Run("sign request failed due to no region found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)

		cfg := aws.Config{
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
			// No region specified to test the error case
		}

		signer, err := osaws.NewSigner(cfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.EqualError(t, err, "aws region cannot be empty")
	})

	t.Run("sign request success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.NoError(t, err)

		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success - port override", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)
		// Original Host should be localhost:9200
		require.Equal(t, "localhost:9200", req.Host)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}

		signer, err := osaws.NewSigner(cfg)
		require.NoError(t, err)

		signer.OverrideSigningPort(443)

		err = signer.SignRequest(req)
		require.NoError(t, err)

		// After port override, Host should be localhost:443
		require.Equal(t, "localhost:443", req.Host)

		q := req.Header

		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		require.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.NoError(t, err)

		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.Equal(t, "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body for OpenSearch Service Serverless", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		require.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSignerWithService(cfg, osaws.OpenSearchServerless)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.NoError(t, err)

		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.Equal(t, "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("new signer failed due to empty service", func(t *testing.T) {
		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		_, err := osaws.NewSignerWithService(cfg, "")
		require.EqualError(t, err, "service cannot be empty")
	})

	t.Run("new signer failed due to blank service", func(t *testing.T) {
		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		_, err := osaws.NewSignerWithService(cfg, "	 ")
		require.EqualError(t, err, "service cannot be empty")
	})

	t.Run("sign request failed due to invalid body", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://localhost:9200", nil)
		require.NoError(t, err)

		body := &brokenReadCloser{err: "boom"}
		req.Body = body

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.EqualError(t, err, "failed to calculate request hash: failed to read request body: boom")
		require.True(t, body.closed, "request body must be closed even when the read fails")
	})
}

// brokenReadCloser fails on Read and records whether Close was called, so a
// test can assert the signer closes the request body on the read-error path.
type brokenReadCloser struct {
	err    string
	closed bool
}

func (b *brokenReadCloser) Read([]byte) (int, error) { return 0, errors.New(b.err) }
func (b *brokenReadCloser) Close() error             { b.closed = true; return nil }
