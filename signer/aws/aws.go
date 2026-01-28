// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Package aws provides AWS request signing for OpenSearch using AWS SDK v2.
//
// BREAKING CHANGE: This package has been migrated from AWS SDK v1 to AWS SDK v2
// due to AWS SDK v1 reaching end-of-support on July 31, 2025.
//
// Migration Guide:
// The API remains largely the same, but you need to update your imports and configuration:
//
// Old (AWS SDK v1):
//
//	import "github.com/aws/aws-sdk-go/aws/session"
//	opts := session.Options{...}
//	signer, err := aws.NewSigner(opts)
//
// New (AWS SDK v2):
//
//	import "github.com/aws/aws-sdk-go-v2/config"
//	cfg, err := config.LoadDefaultConfig(context.TODO())
//	signer, err := aws.NewSigner(cfg)
//
// For more advanced configuration, see: https://aws.github.io/aws-sdk-go-v2/docs/
package aws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	// NOTE: aws-sdk-go v1 is deprecated. Migration to aws-sdk-go-v2 is tracked
	// in a separate issue. These imports will be replaced in a future update.
	// See: https://aws.amazon.com/blogs/developer/announcing-end-of-support-for-aws-sdk-for-go-v1-on-july-31-2025/
	"github.com/aws/aws-sdk-go-v2/aws"
	awsSignerV4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// OpenSearchService Amazon OpenSearch Service Name
const OpenSearchService = "es"

// OpenSearchServerless Amazon OpenSearch Serverless Name
const OpenSearchServerless = "aoss"

const emptyBodySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Signer implements opensearchtransport.Signer using AWS SDK v2
type Signer struct {
	service       string
	signer        *awsSignerV4.Signer
	awsCfg        aws.Config
	signaturePort uint16 // 0-65535
	opts          []func(options *awsSignerV4.SignerOptions)
}

// NewSigner returns an instance of Signer configured for Amazon OpenSearch Service.
// Use NewSignerWithService to configure it for another service such as Amazon OpenSearch Serverless.
//
// BREAKING CHANGE: This function now takes aws.Config instead of session.Options.
// See package documentation for migration guide.
func NewSigner(cfg aws.Config) (*Signer, error) {
	return NewSignerWithService(cfg, OpenSearchService)
}

// NewSignerWithService returns an instance of Signer for a given service.
//
// BREAKING CHANGE: This function now takes aws.Config instead of session.Options.
// See package documentation for migration guide.
//
// Credential caching is automatically enabled to improve performance, especially
// when using STS-based credentials (assume role, web identity, etc.). This reduces
// the number of API calls to AWS STS services and improves signing performance.
func NewSignerWithService(cfg aws.Config, service string) (*Signer, error) {
	if len(strings.TrimSpace(service)) < 1 {
		return nil, errors.New("service cannot be empty")
	}

	// Enable credential caching for better performance, especially with STS credentials.
	// According to AWS SDK v2 documentation, credential caching is not enabled by default
	// and must be explicitly configured using aws.NewCredentialsCache().
	// See: go doc github.com/aws/aws-sdk-go-v2/aws CredentialsCache
	cfg.Credentials = aws.NewCredentialsCache(cfg.Credentials)

	return &Signer{
		service: service,
		signer:  awsSignerV4.NewSigner(),
		awsCfg:  cfg,
	}, nil
}

// OverrideSigningPort allows setting a custom signing port
// useful when going through an SSH Tunnel which would cause a signature mismatch
func (s *Signer) OverrideSigningPort(port uint16) {
	s.signaturePort = port
}

// SignRequest signs the request using SigV4.
func (s *Signer) SignRequest(req *http.Request) error {
	ctx := context.Background()
	t := time.Now()

	creds, err := s.awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve AWS credentials: %w", err)
	}

	if len(s.awsCfg.Region) == 0 {
		return fmt.Errorf("aws region cannot be empty")
	}

	hash, err := hexEncodedSha256OfRequest(req)
	if err != nil {
		return fmt.Errorf("failed to calculate request hash: %w", err)
	}

	// Add the "X-Amz-Content-Sha256" header as required by Amazon OpenSearch Serverless.
	req.Header.Set("X-Amz-Content-Sha256", hash)

	// Apply port override before signing if configured
	if s.signaturePort > 0 {
		req.Host = net.JoinHostPort(req.URL.Hostname(), strconv.Itoa(int(s.signaturePort)))
	}

	err = s.signer.SignHTTP(ctx, creds, req, hash, s.service, s.awsCfg.Region, t, s.opts...)
	if err != nil {
		return err
	}

	// Re-apply port override after signing if the signer modified the Host header
	if s.signaturePort > 0 {
		req.Host = net.JoinHostPort(req.URL.Hostname(), strconv.Itoa(int(s.signaturePort)))
	}

	return nil
}

func hexEncodedSha256OfRequest(r *http.Request) (string, error) {
	if r.Body == nil {
		return emptyBodySHA256, nil
	}

	hasher := sha256.New()

	reqBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read request body: %w", err)
	}

	if err := r.Body.Close(); err != nil {
		return "", fmt.Errorf("failed to close request body: %w", err)
	}

	r.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
	hasher.Write(reqBodyBytes)
	digest := hasher.Sum(nil)

	return hex.EncodeToString(digest), nil
}
