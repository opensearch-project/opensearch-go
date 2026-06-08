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
	"errors"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/require"

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

const (
	testRegion = "us-west-2"
)

func TestV4SignerAwsSdkV2(t *testing.T) {
	currentRegion := os.Getenv("AWS_REGION")

	os.Setenv("AWS_REGION", testRegion)

	defaultRegion := os.Getenv("AWS_DEFAULT_REGION")

	os.Unsetenv("AWS_DEFAULT_REGION")

	t.Cleanup(func() {
		os.Setenv("AWS_DEFAULT_REGION", defaultRegion)
		os.Setenv("AWS_REGION", currentRegion)
	})

	t.Run("sign request failed due to no region found", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Unsetenv("AWS_REGION")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
			config.WithRegion(""),
		)
		require.NoError(t, err)

		awsCfg.Region = "" // Ensure region is empty to trigger error

		signer, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)
		err = signer.SignRequest(req)

		require.EqualErrorf(
			t, err, "aws region cannot be empty", "unexpected error")
	})

	t.Run("sign request success", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.NoError(t, err)

		q := req.Header
		require.Equal(t, "localhost:9200", req.Host)
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("with signature port override", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "https://localhost:9200", nil)
		require.NoError(t, err)
		require.Equal(t, "localhost:9200", req.Host)

		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)

		signer.OverrideSigningPort(443)
		err = signer.SignRequest(req)
		require.NoError(t, err)

		// Should have stripped off the port given it was 443 (80 would have also gotten removed)
		require.Equal(t, "localhost", req.Host)
		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		require.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.NoError(t, err)
		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request success with body for other AWS Services", func(t *testing.T) {
		req, err := http.NewRequest(
			http.MethodPost, "https://localhost:9200",
			bytes.NewBufferString("some data"))
		require.NoError(t, err)
		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		signer, err := awsv2.NewSignerWithService(awsCfg, "ec")
		require.NoError(t, err)

		require.NoError(t, err)
		err = signer.SignRequest(req)
		require.NoError(t, err)
		q := req.Header
		require.NotEmpty(t, q.Get("Authorization"))
		require.NotEmpty(t, q.Get("X-Amz-Date"))
		require.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
	})

	t.Run("sign request failed due to invalid service", func(t *testing.T) {
		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		_, err = awsv2.NewSignerWithService(awsCfg, "")
		require.EqualError(t, err, "service cannot be empty")
	})

	t.Run("closes request body when read fails", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, "https://localhost:9200", nil)
		require.NoError(t, err)

		body := &brokenReadCloser{err: "boom"}
		req.Body = body

		region := os.Getenv("AWS_REGION")
		os.Setenv("AWS_REGION", "us-west-2")
		t.Cleanup(func() {
			os.Setenv("AWS_REGION", region)
		})

		awsCfg, err := config.LoadDefaultConfig(context.TODO(),
			config.WithRegion("us-west-2"),
			config.WithCredentialsProvider(
				getCredentialProvider(),
			),
		)
		require.NoError(t, err)

		signer, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)

		err = signer.SignRequest(req)
		require.Error(t, err)
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
