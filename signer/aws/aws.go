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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	v4 "github.com/aws/aws-sdk-go/aws/signer/v4"
)

// OpenSearchService Amazon OpenSearch Service Name
const OpenSearchService = "es"

// OpenSearchServerless Amazon OpenSearch Serverless Name
const OpenSearchServerless = "aoss"

const emptyBodySHA256 = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"

// Signer is an interface that will implement opensearchtransport.Signer
type Signer struct {
	session session.Session
	service string
}

// NewSigner returns an instance of Signer for configured for Amazon OpenSearch Service.
// Use NewSignerWithService to configure it for another service such as Amazon OpenSearch Serverless.
func NewSigner(opts session.Options) (*Signer, error) {
	return NewSignerWithService(opts, OpenSearchService)
}

// NewSignerWithService returns an instance of Signer for a given service.
func NewSignerWithService(opts session.Options, service string) (*Signer, error) {
	if len(strings.TrimSpace(service)) < 1 {
		return nil, errors.New("service cannot be empty")
	}

	awsSession, err := session.NewSessionWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get session from given options %v: %w", opts, err)
	}

	return &Signer{
		session: *awsSession,
		service: service,
	}, nil
}

// SignRequest signs the request using SigV4.
func (s Signer) SignRequest(req *http.Request) error {
	return sign(req, s.session.Config.Region, s.service, v4.NewSigner(s.session.Config.Credentials))
}

func sign(req *http.Request, region *string, serviceName string, signer *v4.Signer) error {
	if region == nil || len(*region) == 0 {
		return fmt.Errorf("aws region cannot be empty")
	}

	var body io.ReadSeeker

	contentSha256Hash := emptyBodySHA256

	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		if err != nil {
			return fmt.Errorf("failed to read request body: %w", err)
		}

		body = bytes.NewReader(b)

		hash, err := hexEncodedSha256(b)
		if err != nil {
			return fmt.Errorf("failed to calculate hash of request body: %w", err)
		}

		contentSha256Hash = hash
	}
	// Add the "X-Amz-Content-Sha256" header as required by Amazon OpenSearch Serverless.
	req.Header.Set("X-Amz-Content-Sha256", contentSha256Hash)

	if _, err := signer.Sign(req, body, serviceName, *region, time.Now().UTC()); err != nil {
		return err
	}

	return nil
}

func hexEncodedSha256(b []byte) (string, error) {
	hasher := sha256.New()

	if _, err := hasher.Write(b); err != nil {
		return "", fmt.Errorf("failed to write: %w", err)
	}

	digest := hasher.Sum(nil)

	return hex.EncodeToString(digest), nil
}
