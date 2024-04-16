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

package awsv2_test

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v4/signer/awsv2"
)

func getCredentialProvider() aws.CredentialsProviderFunc {
	return func(ctx context.Context) (aws.Credentials, error) {
		c := &aws.Credentials{
			AccessKeyID:     "AKID",
			SecretAccessKey: "SECRET_KEY",
			SessionToken:    "TOKEN",
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
				getCredentialProvider(),
			),
			config.WithRegion(""),
		)
		assert.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
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
				getCredentialProvider(),
			),
		)
		assert.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)

		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})
	t.Run("sign request success with body", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		assert.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		assert.NoError(t, err)

		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body for other AWS Services", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		assert.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		defer func() {
			os.Setenv("AWS_REGION", region)
		}()

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		assert.NoError(t, err)

		signer, err := awsv2.NewSignerWithService(awsCfg, "ec")
		assert.NoError(t, err)

		assert.NoError(t, err)
		err = signer.SignRequest(req)
		assert.NoError(t, err)
		q := req.Header
		assert.NotEmpty(t, q.Get("Authorization"))
		assert.NotEmpty(t, q.Get("X-Amz-Date"))
		assert.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request failed due to invalid service", func(t *testing.T) {
		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		assert.NoError(t, err)

		_, err = awsv2.NewSignerWithService(awsCfg, "")
		assert.EqualError(t, err, "service cannot be empty")
	})
}
