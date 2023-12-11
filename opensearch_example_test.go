// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

//go:build !integration

package opensearch_test

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v3/opensearchtransport"
)

func init() {
	log.SetFlags(0)
}

func ExampleNewDefaultClient() {
	ctx := context.Background()
	client, err := opensearchapi.NewDefaultClient()
	if err != nil {
		log.Fatalf("Error creating the client: %s\n", err)
	}

	_, err = client.Info(ctx, nil)
	if err != nil {
		log.Fatalf("Error getting the response: %s\n", err)
	}

	log.Print(client.Client.Transport.(*opensearchtransport.Client).URLs())
}

func ExampleNewClient() {
	cfg := opensearchapi.Config{
		Client: opensearch.Config{
			Addresses: []string{
				"http://localhost:9200",
			},
			Username: "foo",
			Password: "bar",
			Transport: &http.Transport{
				MaxIdleConnsPerHost:   10,
				ResponseHeaderTimeout: time.Second,
				DialContext:           (&net.Dialer{Timeout: time.Second}).DialContext,
				TLSClientConfig: &tls.Config{
					MinVersion: tls.VersionTLS12,
				},
			},
		},
	}

	client, _ := opensearchapi.NewClient(cfg)
	log.Print(client.Client.Transport.(*opensearchtransport.Client).URLs())
}

func ExampleNewClient_logger() {
	// import "github.com/opensearch-project/opensearch-go/opensearchtransport"
	// Use one of the bundled loggers:
	//
	// * opensearchtransport.TextLogger
	// * opensearchtransport.ColorLogger
	// * opensearchtransport.CurlLogger
	// * opensearchtransport.JSONLogger
	cfg := opensearchapi.Config{
		Client: opensearch.Config{
			Logger: &opensearchtransport.ColorLogger{Output: os.Stdout},
		},
	}

	opensearchapi.NewClient(cfg)
}
