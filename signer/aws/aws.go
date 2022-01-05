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
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	v4 "github.com/aws/aws-sdk-go/aws/signer/v4"
)

// OpenSearchService AWS OpenSearchService Name
const OpenSearchService = "es"

// Signer is a interface that will implement opensearchtransport.Signer
type Signer struct {
	session session.Session
	service string
}

// NewSigner returns an instance of Signer
func NewSigner(opts session.Options) (*Signer, error) {
	awsSession, err := session.NewSessionWithOptions(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to get session from given option %v due to %s", opts, err)
	}
	return &Signer{
		session: *awsSession,
		service: OpenSearchService,
	}, nil
}

// SignRequest signs the request using SigV4
func (s Signer) SignRequest(req *http.Request) error {
	signer := v4.NewSigner(s.session.Config.Credentials)
	return sign(req, s.session.Config.Region, s.service, signer)

}

func sign(req *http.Request, region *string, serviceName string, signer *v4.Signer) (err error) {
	if region == nil || len(*region) == 0 {
		return fmt.Errorf("aws region cannot be empty")
	}
	if req.Body == nil {
		_, err = signer.Sign(req, nil, serviceName, *region, time.Now().UTC())
		return
	}
	buf, err := io.ReadAll(req.Body)
	if err != nil {
		return err
	}
	_, err = signer.Sign(req, bytes.NewReader(buf), serviceName, *region, time.Now().UTC())
	return
}
