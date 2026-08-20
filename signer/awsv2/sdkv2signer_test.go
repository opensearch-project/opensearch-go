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
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/signer"
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

func loadTestAWSConfig(t *testing.T, creds aws.CredentialsProvider) aws.Config {
	t.Helper()
	if creds == nil {
		creds = getCredentialProvider()
	}
	awsCfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(testRegion),
		config.WithCredentialsProvider(creds),
	)
	require.NoError(t, err)
	return awsCfg
}

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

		s, err := awsv2.NewSigner(awsCfg)
		require.NoError(t, err)
		err = s.SignRequest(req)

		require.EqualErrorf(
			t, err, "aws region cannot be empty", "unexpected error")
	})

	successCases := []struct {
		name         string
		method       string
		body         string
		service      string
		overridePort uint16
		wantHost     string
	}{
		{
			name:     "sign request success",
			method:   http.MethodGet,
			wantHost: "localhost:9200",
		},
		{
			name:         "with signature port override",
			method:       http.MethodGet,
			overridePort: 443,
			wantHost:     "localhost",
		},
		{
			name:     "sign request success with body",
			method:   http.MethodPost,
			body:     "some data",
			wantHost: "localhost:9200",
		},
		{
			name:     "sign request success with body for other AWS Services",
			method:   http.MethodPost,
			body:     "some data",
			service:  "ec",
			wantHost: "localhost:9200",
		},
	}

	for _, tt := range successCases {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = bytes.NewBufferString(tt.body)
			}
			req, err := http.NewRequest(tt.method, "https://localhost:9200", body)
			require.NoError(t, err)

			awsCfg := loadTestAWSConfig(t, nil)

			var s signer.Signer
			if tt.service != "" {
				s, err = awsv2.NewSignerWithService(awsCfg, tt.service)
			} else {
				s, err = awsv2.NewSigner(awsCfg)
			}
			require.NoError(t, err)

			if tt.overridePort != 0 {
				s.OverrideSigningPort(tt.overridePort)
			}
			require.NoError(t, s.SignRequest(req))

			require.Equal(t, tt.wantHost, req.Host)
			q := req.Header
			require.NotEmpty(t, q.Get("Authorization"))
			require.NotEmpty(t, q.Get("X-Amz-Date"))
			require.NotEmpty(t, q.Get("X-Amz-Content-Sha256"))
		})
	}

	t.Run("sign request failed due to invalid service", func(t *testing.T) {
		awsCfg := loadTestAWSConfig(t, nil)

		_, err := awsv2.NewSignerWithService(awsCfg, "")
		require.EqualError(t, err, "service cannot be empty")
	})

	signFailCases := []struct {
		name  string
		setup func(t *testing.T) (*http.Request, aws.Config)
		check func(t *testing.T, err error, req *http.Request)
	}{
		{
			name: "closes request body when read fails",
			setup: func(t *testing.T) (*http.Request, aws.Config) {
				t.Helper()
				req, err := http.NewRequest(http.MethodPost, "https://localhost:9200", nil)
				require.NoError(t, err)
				req.Body = &brokenReadCloser{err: "boom"}
				return req, loadTestAWSConfig(t, nil)
			},
			check: func(t *testing.T, err error, req *http.Request) {
				t.Helper()
				require.Error(t, err)
				body, ok := req.Body.(*brokenReadCloser)
				require.True(t, ok)
				require.True(t, body.closed, "request body must be closed even when the read fails")
			},
		},
		{
			name: "honors a cancelled request context for credential retrieval",
			setup: func(t *testing.T) (*http.Request, aws.Config) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				awsCfg := loadTestAWSConfig(t, aws.CredentialsProviderFunc(
					func(ctx context.Context) (aws.Credentials, error) {
						if err := ctx.Err(); err != nil {
							return aws.Credentials{}, err
						}
						return aws.Credentials{
							AccessKeyID:     "AKID",
							SecretAccessKey: "SECRET_KEY",
						}, nil
					},
				))

				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://localhost:9200", nil)
				require.NoError(t, err)
				return req, awsCfg
			},
			check: func(t *testing.T, err error, _ *http.Request) {
				t.Helper()
				require.ErrorIs(t, err, context.Canceled)
			},
		},
	}

	for _, tt := range signFailCases {
		t.Run(tt.name, func(t *testing.T) {
			req, awsCfg := tt.setup(t)
			s, err := awsv2.NewSigner(awsCfg)
			require.NoError(t, err)
			tt.check(t, s.SignRequest(req), req)
		})
	}
}

// brokenReadCloser fails on Read and records whether Close was called, so a
// test can assert the signer closes the request body on the read-error path.
type brokenReadCloser struct {
	err    string
	closed bool
}

func (b *brokenReadCloser) Read([]byte) (int, error) { return 0, errors.New(b.err) }
func (b *brokenReadCloser) Close() error             { b.closed = true; return nil }
