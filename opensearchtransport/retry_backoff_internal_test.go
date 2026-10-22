// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport/testutil/mockhttp"
)

func TestJitteredRetryBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		attempt int
		ceiling time.Duration
	}{
		{name: "first retry", attempt: 1, ceiling: 100 * time.Millisecond},
		{name: "second retry doubles the ceiling", attempt: 2, ceiling: 200 * time.Millisecond},
		{name: "third retry", attempt: 3, ceiling: 400 * time.Millisecond},
		{name: "a huge attempt is capped, not overflowed", attempt: 1000, ceiling: defaultRetryBackoffBase << maxRetryBackoffShift},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for range 200 {
				d := jitteredRetryBackoff(tt.attempt)
				require.GreaterOrEqual(t, d, time.Duration(0))
				require.LessOrEqual(t, d, tt.ceiling)
			}
		})
	}
}

// TestRetryBudget: a retry whose delay would take the request's delays past the
// retry budget is not made, and a negative budget sets no bound.
func TestRetryBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		budget    time.Duration
		delay     time.Duration
		wantCalls int
	}{
		{name: "delays within the budget run every retry", budget: time.Second, delay: time.Millisecond, wantCalls: 1 + defaultMaxRetries},
		{name: "the retry that would pass the budget is not made", budget: 5 * time.Millisecond, delay: 3 * time.Millisecond, wantCalls: 2},
		{name: "a first delay past the budget makes no retry", budget: time.Millisecond, delay: 2 * time.Millisecond, wantCalls: 1},
		{name: "a negative budget sets no bound", budget: -1, delay: 3 * time.Millisecond, wantCalls: 1 + defaultMaxRetries},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls int
			c, err := New(Config{
				URLs: []*url.URL{{}},
				Transport: mockhttp.NewRoundTripFunc(t, func(*http.Request) (*http.Response, error) {
					calls++
					return &http.Response{StatusCode: http.StatusBadGateway, Status: "MOCK"}, nil
				}),
				RetryBackoff:      func(int) time.Duration { return tt.delay },
				RetryBudget:       tt.budget,
				NodeStatsInterval: -1,
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = c.Close() })

			res, err := c.Stream(&http.Request{URL: &url.URL{}, Header: make(http.Header)})
			require.NoError(t, err)
			if res != nil && res.Body != nil {
				_ = res.Body.Close()
			}

			require.Equal(t, tt.wantCalls, calls)
		})
	}
}
