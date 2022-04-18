// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

package aws

import (
	"bytes"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"

	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/stretchr/testify/assert"
)

func TestV4Signer(t *testing.T) {
	t.Run("sign request failed due to no region found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()
		sessionOptions := session.Options{
			Config: aws.Config{
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := NewSigner(sessionOptions)
		assert.NoError(t, err)
		err = signer.SignRequest(req)

		assert.EqualErrorf(
			t, err, "aws region cannot be empty", "unexpected error")
	})
	t.Run("sign request success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := NewSigner(sessionOptions)
		assert.NoError(t, err)
		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
	})

	t.Run("sign request success with body", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBuffer([]byte(`some data`)))
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := NewSigner(sessionOptions)
		assert.NoError(t, err)
		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
	})

	t.Run("sign request success with body for other AWS Services", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBuffer([]byte(`some data`)))
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		signer, err := NewSignerWithService(sessionOptions, "ec")
		assert.NoError(t, err)
		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
	})

	t.Run("sign request failed due to invalid service", func(t *testing.T) {
		sessionOptions := session.Options{
			Config: aws.Config{
				Region:      aws.String("us-west-2"),
				Credentials: credentials.NewStaticCredentials("AKID", "SECRET_KEY", "TOKEN"),
			},
		}
		_, err := NewSignerWithService(sessionOptions, "")
		assert.EqualError(t, err, "service cannot be empty")

	})
}
