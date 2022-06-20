// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

package awsv2

import (
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"
	"net/http"
	"os"
	"testing"
)

func getCredentialProvider(accessKey, secretAccessKey, token string) aws.CredentialsProviderFunc {
	return func(ctx context.Context) (aws.Credentials, error) {
		c := &aws.Credentials{
			AccessKeyID:     accessKey,
			SecretAccessKey: secretAccessKey,
			SessionToken:    token,
		}
		return *c, nil
	}
}

func TestV4SignerAwsSdkV2(t *testing.T) {
	t.Run("sign request failed due to no region found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()
		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithCredentialsProvider(
				getCredentialProvider("AKID", "SECRET_KEY", "TOKEN"),
			),
			config.WithRegion(""),
		)
		assert.NoError(t, err)

		signer, err := NewSigner(awsCfg)
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

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider("AKID", "SECRET_KEY", "TOKEN"),
			),
		)
		assert.NoError(t, err)

		signer, err := NewSigner(awsCfg)
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

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider("AKID", "SECRET_KEY", "TOKEN"),
			),
		)
		assert.NoError(t, err)

		signer, err := NewSigner(awsCfg)
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

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider("AKID", "SECRET_KEY", "TOKEN"),
			),
		)
		assert.NoError(t, err)

		signer, err := NewSignerWithService(awsCfg, "ec")
		assert.NoError(t, err)

		assert.NoError(t, err)
		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
	})

	t.Run("sign request failed due to invalid service", func(t *testing.T) {
		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider("AKID", "SECRET_KEY", "TOKEN"),
			),
		)
		assert.NoError(t, err)

		_, err = NewSignerWithService(awsCfg, "")
		assert.EqualError(t, err, "service cannot be empty")
	})
}
