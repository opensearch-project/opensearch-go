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

//go:build integration && core && !multinode

package opensearch_test

import (
	"context"
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4"
	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

func TestClientTransport(t *testing.T) {
	/*
		t.Run("Persistent", func(t *testing.T) {
			client, err := ostest.NewClient(t)
			if err != nil {
				t.Fatalf("Error creating the client: %s", err)
			}

			var total int

			for i := 0; i < 101; i++ {
				var curTotal int

				res, err := client.Nodes.Stats(nil, &opensearchapi.NodesStatsReq{Metric: []string{"http"}})
				if err != nil {
					t.Fatalf("Unexpected error: %s", err)
				}

				for _, v := range res.Nodes {
					curTotal = v.HTTP.TotalOpened
					break
				}

				if curTotal < 1 {
					t.Errorf("Unexpected total_opened: %d", curTotal)
				}
				if total == 0 {
					total = curTotal
				}

				if total != curTotal {
					t.Errorf("Expected total_opened=%d, got: %d", total, curTotal)
				}
			}

			log.Printf("total_opened: %d", total)
		})
	*/

	t.Run("Concurrent", func(t *testing.T) {
		var wg sync.WaitGroup

		client, err := ostest.NewClient(t)
		if err != nil {
			t.Fatalf("Error creating the client: %s", err)
		}

		for i := 0; i < 101; i++ {
			wg.Add(1)
			time.Sleep(10 * time.Millisecond)

			go func(i int) {
				defer wg.Done()
				_, err := client.Info(nil, nil)
				if err != nil {
					t.Errorf("Unexpected error: %s", err)
				}
			}(i)
		}
		wg.Wait()
	})

	t.Run("WithContext", func(t *testing.T) {
		client, err := ostest.NewClient(t)
		if err != nil {
			t.Fatalf("Error creating the client: %s", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
		defer cancel()

		_, err = client.Info(ctx, nil)
		require.Error(t, err, "Expected context deadline exceeded error")
	})

	t.Run("Configured", func(t *testing.T) {
		cfg := opensearchapi.Config{
			Client: opensearch.Config{
				Transport: &http.Transport{
					MaxIdleConnsPerHost:   10,
					ResponseHeaderTimeout: time.Second,
					DialContext:           (&net.Dialer{Timeout: time.Nanosecond}).DialContext,
					TLSClientConfig: &tls.Config{
						MinVersion:         tls.VersionTLS11,
						InsecureSkipVerify: true,
					},
				},
			},
		}

		client, err := opensearchapi.NewClient(cfg)
		if err != nil {
			t.Fatalf("Error creating the client: %s", err)
		}

		_, err = client.Info(nil, nil)
		if err == nil {
			t.Fatalf("Expected error, but got: %v", err)
		}
		if _, ok := err.(*net.OpError); !ok {
			t.Fatalf("Expected net.OpError, but got: %T", err)
		}
	})
}

type CustomTransport struct {
	client *http.Client
	logger func(format string, v ...any)
}

func (t *CustomTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-Foo", "bar")
	if t.logger != nil {
		t.logger("> %s %q %q", req.Method, req.URL.String(), req.Header)
	}
	return t.client.Do(req)
}

func TestClientCustomTransport(t *testing.T) {
	t.Run("Customized", func(t *testing.T) {
		client, err := opensearchapi.NewDefaultClient()
		require.Nil(t, err)

		cfg, err := ostest.ClientConfig()
		require.Nil(t, err)

		if cfg != nil {
			cfg.Client.Transport = &CustomTransport{
				client: &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
					},
				},
				logger: func(format string, v ...any) {
					if testutil.IsDebugEnabled(t) {
						t.Logf(format, v...)
					}
				},
			}
			client, err = opensearchapi.NewClient(*cfg)
			require.Nil(t, err)

			// Wait for cluster to be ready before running tests
			err = ostest.WaitForClusterReady(t, client)
			require.Nil(t, err)
		}

		for i := 0; i < 10; i++ {
			_, err := client.Info(nil, nil)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
		}
	})

	t.Run("Manual", func(t *testing.T) {
		tp, _ := opensearchtransport.New(opensearchtransport.Config{
			URLs: []*url.URL{
				{Scheme: "http", Host: "localhost:9200"},
			},
			Transport: http.DefaultTransport,
		})
		config, err := ostest.ClientConfig()
		if err != nil {
			t.Fatalf("Error getting config: %s", err)
		}
		if ostest.IsSecure() {
			tp, _ = opensearchtransport.New(opensearchtransport.Config{
				URLs: []*url.URL{
					{Scheme: "https", Host: "localhost:9200"},
				},
				Transport: config.Client.Transport,
				Username:  config.Client.Username,
				Password:  config.Client.Password,
			})
		}

		client := opensearchapi.Client{
			Client: &opensearch.Client{
				Transport: tp,
			},
		}

		// Simple readiness wait for manually-constructed client (only uses Info API)
		ctx := t.Context()
		for {
			_, err := client.Info(ctx, nil)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("Cluster not ready: %s", ctx.Err())
			case <-time.After(5 * time.Second):
				// Retry
			}
		}

		for i := 0; i < 10; i++ {
			_, err := client.Info(nil, nil)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
		}
	})
}

type ReplacedTransport struct {
	counter uint64
}

func (t *ReplacedTransport) Perform(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = "http"
	req.URL.Host = "localhost:9200"
	config, err := ostest.ClientConfig()
	if err != nil {
		return nil, err
	}
	if ostest.IsSecure() {
		req.URL.Scheme = "https"
		req.SetBasicAuth(config.Client.Username, config.Client.Password)
	}

	atomic.AddUint64(&t.counter, 1)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return transport.RoundTrip(req)
}

func (t *ReplacedTransport) Count() uint64 {
	return atomic.LoadUint64(&t.counter)
}

func TestClientReplaceTransport(t *testing.T) {
	t.Run("Replaced", func(t *testing.T) {
		const expectedRequests = 10

		tr := &ReplacedTransport{}
		client := opensearchapi.Client{
			Client: &opensearch.Client{
				Transport: tr,
			},
		}

		// Simple readiness wait for manually-constructed client (only uses Info API)
		ctx := t.Context()
		for {
			_, err := client.Info(ctx, nil)
			if err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatalf("Cluster not ready: %s", ctx.Err())
			case <-time.After(5 * time.Second):
				// Retry
			}
		}

		// Reset counter after readiness check
		initialCount := tr.Count()

		for i := 0; i < expectedRequests; i++ {
			_, err := client.Info(nil, nil)
			if err != nil {
				t.Fatalf("Unexpected error: %s", err)
			}
		}

		actualRequests := tr.Count() - initialCount
		if actualRequests > expectedRequests {
			t.Errorf("Expected at most %d requests, got=%d", expectedRequests, actualRequests)
		}
	})
}

func TestClientAPI(t *testing.T) {
	t.Run("Info", func(t *testing.T) {
		client, err := ostest.NewClient(t)
		if err != nil {
			log.Fatalf("Error creating the client: %s\n", err)
		}

		res, err := client.Info(nil, nil)
		if err != nil {
			log.Fatalf("Error getting the response: %s\n", err)
		}
		if res.ClusterName == "" {
			log.Fatalf("cluster_name is empty: %s\n", err)
		}
	})
}
