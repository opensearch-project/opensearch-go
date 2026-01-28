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
	"errors"
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

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
	ostestutil "github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchutil/testutil/mockhttp"
)

func TestClientTransport(t *testing.T) {
	/*
		t.Run("Persistent", func(t *testing.T) {
			client, err := testutil.NewClient(t)
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

		client, err := testutil.NewClient(t)
		require.NoError(t, err)

		for i := range 101 {
			wg.Add(1)
			time.Sleep(10 * time.Millisecond)

			go func(_ int) {
				defer wg.Done()
				_, err := client.Info(t.Context(), nil)
				require.NoError(t, err)
			}(i)
		}
		wg.Wait()
	})

	t.Run("WithContext", func(t *testing.T) {
		client, err := testutil.NewClient(t)
		require.NoError(t, err)

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
		require.NoError(t, err)

		_, err = client.Info(t.Context(), nil)
		require.Error(t, err)
		opError := &net.OpError{}
		if !errors.As(err, &opError) {
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
		require.NoError(t, err)

		cfg, err := testutil.ClientConfig(t)
		require.NoError(t, err)

		if cfg != nil {
			cfg.Client.Transport = &CustomTransport{
				client: &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
					},
				},
				logger: func(format string, v ...any) {
					if ostestutil.IsDebugEnabled(t) {
						t.Logf(format, v...)
					}
				},
			}
			client, err = opensearchapi.NewClient(*cfg)
			require.NoError(t, err)

			// Wait for cluster to be ready before running tests
			err = testutil.WaitForClusterReady(t, client)
			require.NoError(t, err)
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
	})

	t.Run("Manual", func(t *testing.T) {
		config, err := testutil.ClientConfig(t)
		require.NoError(t, err)

		// Use centralized URL construction
		u := mockhttp.GetOpenSearchURL(t)
		tp, _ := opensearchtransport.New(opensearchtransport.Config{
			URLs:      []*url.URL{u},
			Transport: config.Client.Transport,
			Username:  config.Client.Username,
			Password:  config.Client.Password,
		})

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
	})
}

type TestTransport struct {
	counter atomic.Uint64
	t       *testing.T
}

func (tr *TestTransport) Perform(req *http.Request) (*http.Response, error) {
	// Use centralized URL construction
	u := mockhttp.GetOpenSearchURL(tr.t)
	req.URL.Scheme = u.Scheme
	req.URL.Host = u.Host

	config, err := testutil.ClientConfig(tr.t)
	if err != nil {
		return nil, err
	}
	if testutil.IsSecure(tr.t) {
		req.SetBasicAuth(config.Client.Username, config.Client.Password)
	}

	tr.counter.Add(1)
	transport := config.Client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(req)
}

func (tr *TestTransport) Count() uint64 {
	return tr.counter.Load()
}

func TestClientReplaceTransport(t *testing.T) {
	t.Run("Replaced", func(t *testing.T) {
		const expectedRequests = 10

		tr := &TestTransport{t: t}
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
				t.Fatalf("Cluster not ready: %v", ctx.Err())
			case <-time.After(5 * time.Second):
				// Retry
			}
		}

		// Reset counter after readiness check
		initialCount := tr.Count()

		for range expectedRequests {
			_, err := client.Info(t.Context(), nil)
			require.NoError(t, err)
		}

		actualRequests := tr.Count() - initialCount
		if actualRequests > expectedRequests {
			t.Errorf("Expected at most %d requests, got=%d", expectedRequests, actualRequests)
		}
	})
}

func TestClientAPI(t *testing.T) {
	t.Run("Info", func(t *testing.T) {
		client, err := testutil.NewClient(t)
		require.NoError(t, err)

		res, err := client.Info(t.Context(), nil)
		require.NoError(t, err)
		if res.ClusterName == "" {
			log.Fatalf("cluster_name is empty: %s\n", err)
		}
	})
}
