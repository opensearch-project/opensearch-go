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

	//nolint:staticcheck // SA1019: Keeping aws-sdk-go v1 for backwards compatibility
	"github.com/aws/aws-sdk-go/aws"
	//nolint:staticcheck // SA1019: Keeping aws-sdk-go v1 for backwards compatibility
	"github.com/aws/aws-sdk-go/aws/credentials"
	//nolint:staticcheck // SA1019: Keeping aws-sdk-go v1 for backwards compatibility
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/stretchr/testify/assert"

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

		sessionOptions := session.Options{
			Config: aws.Config{
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}

		signer, err := osaws.NewSigner(sessionOptions)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.EqualError(t, err, "aws region cannot be empty")
	})

	t.Run("sign request success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)

		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := osaws.NewSigner(sessionOptions)
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
		assert.NoError(t, err)
		assert.Equal(t, "localhost:9200", req.Host)

		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}

		signer, err := osaws.NewSigner(sessionOptions)
		assert.NoError(t, err)

		signer.OverrideSigningPort(443)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		assert.Equal(t, "localhost", req.Host) // should have been stripped off given we used a common port

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

		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := osaws.NewSigner(sessionOptions)
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

		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := osaws.NewSignerWithService(sessionOptions, osaws.OpenSearchServerless)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.Equal(t, "1307990e6ba5ca145eb35e99182a9bec46531bc54ddf656a602c780fa0240dee", q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("new signer failed due to empty service", func(t *testing.T) {
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		_, err := osaws.NewSignerWithService(sessionOptions, "")
		assert.EqualError(t, err, "service cannot be empty")
	})

	t.Run("new signer failed due to blank service", func(t *testing.T) {
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		_, err := osaws.NewSignerWithService(sessionOptions, "	 ")
		assert.EqualError(t, err, "service cannot be empty")
	})

	t.Run("sign request failed due to invalid body", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://localhost:9200", nil)
		assert.NoError(t, err)

		req.Body = io.NopCloser(brokenReader("boom"))

		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := osaws.NewSigner(sessionOptions)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.EqualError(t, err, "failed to read request body: boom")
	})
}

type brokenReader string

func (br brokenReader) Read([]byte) (int, error) {
	return 0, errors.New(string(br))
}
