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
	"io"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	osaws "github.com/opensearch-project/opensearch-go/v4/signer/aws"
)

func TestConstants(t *testing.T) {
	assert.Equal(t, "es", osaws.OpenSearchService)
	assert.Equal(t, "aoss", osaws.OpenSearchServerless)
}

func TestV4Signer(t *testing.T) {
	t.Run("sign request failed due to no region found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)

		cfg := aws.Config{
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
			// No region specified to test the error case
		}

		signer, err := osaws.NewSigner(cfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.EqualError(t, err, "aws region cannot be empty")
	})

	t.Run("sign request success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", q.Get("X-Amz-Content-Sha256"))
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

		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		assert.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.Equal(t, "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body for OpenSearch Service Serverless", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		assert.NoError(t, err)

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSignerWithService(cfg, osaws.OpenSearchServerless)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.Equal(t, "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("new signer failed due to empty service", func(t *testing.T) {
		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		_, err := osaws.NewSignerWithService(cfg, "")
		assert.EqualError(t, err, "service cannot be empty")
	})

	t.Run("new signer failed due to blank service", func(t *testing.T) {
		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		_, err := osaws.NewSignerWithService(cfg, "	 ")
		assert.EqualError(t, err, "service cannot be empty")
	})

	t.Run("sign request failed due to invalid body", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://localhost:9200", nil)
		assert.NoError(t, err)

		req.Body = io.NopCloser(brokenReader("boom"))

		cfg := aws.Config{
			Region:      "us-west-2",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET_KEY", "TOKEN"),
		}
		signer, err := osaws.NewSigner(cfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.EqualError(t, err, "failed to calculate request hash: failed to read request body: boom")
	})
}

type brokenReader string

func (br brokenReader) Read([]byte) (int, error) {
	return 0, errors.New(string(br))
}
