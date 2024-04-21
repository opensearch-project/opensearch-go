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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsSignerV4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/opensearch-project/opensearch-go/v4/signer"
)

const (
	openSearchService = "es"
	//nolint:gosec // static empty Body
	emptyStringSHA256 = `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`
)

type awsSdkV2Signer struct {
	service string
	signer  *awsSignerV4.Signer
	awsCfg  aws.Config
}

// NewSigner returns an instance of Signer for AWS OpenSearchService
func NewSigner(cfg aws.Config) (signer.Signer, error) {
	return NewSignerWithService(cfg, openSearchService)
}

// NewSignerWithService returns an instance of Signer for given service
func NewSignerWithService(cfg aws.Config, service string) (signer.Signer, error) {
	if len(strings.TrimSpace(service)) < 1 {
		return nil, errors.New("service cannot be empty")
	}

	return &awsSdkV2Signer{
		service: service,
		signer:  awsSignerV4.NewSigner(),
		awsCfg:  cfg,
	}, nil
}

// SignRequest adds headers to the request
func (s *awsSdkV2Signer) SignRequest(r *http.Request) error {
	ctx := context.Background()
	t := time.Now()

	creds, err := s.awsCfg.Credentials.Retrieve(ctx)
	if err != nil {
		return err
	}

	if len(s.awsCfg.Region) == 0 {
		return fmt.Errorf("aws region cannot be empty")
	}

	hash, err := hexEncodedSha256OfRequest(r)
	r.Header.Set("X-Amz-Content-Sha256", hash)

	if err != nil {
		return err
	}

	return s.signer.SignHTTP(ctx, creds, r, hash, s.service, s.awsCfg.Region, t)
}

func hexEncodedSha256OfRequest(r *http.Request) (string, error) {
	if r.Body == nil {
		return emptyStringSHA256, nil
	}

	hasher := sha256.New()

	reqBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}

	if err := r.Body.Close(); err != nil {
		return "", err
	}

	r.Body = io.NopCloser(bytes.NewBuffer(reqBodyBytes))
	hasher.Write(reqBodyBytes)
	digest := hasher.Sum(nil)

	return hex.EncodeToString(digest), nil
}
