// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectPreference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		url            string
		wantPreference string // expected value of "preference" param after injection
		wantRawQuery   string // if non-empty, assert exact RawQuery match
		wantNoChange   bool   // if true, RawQuery should be unchanged
	}{
		{
			name:           "empty query string adds preference=_local",
			url:            "http://localhost:9200/_search",
			wantPreference: "_local",
			wantRawQuery:   "preference=_local",
		},
		{
			name:           "existing params no preference appends preference=_local",
			url:            "http://localhost:9200/_search?size=10",
			wantPreference: "_local",
		},
		{
			name:           "existing preference param is not overridden",
			url:            "http://localhost:9200/_search?preference=_primary",
			wantNoChange:   true,
			wantPreference: "_primary",
		},
		{
			name:           "preference param with custom value is preserved",
			url:            "http://localhost:9200/_search?preference=_custom_value",
			wantNoChange:   true,
			wantPreference: "_custom_value",
		},
		{
			name:           "URL with fragment handles correctly",
			url:            "http://localhost:9200/_search#section",
			wantPreference: "_local",
		},
		{
			name:           "multiple existing params preserves all and adds preference",
			url:            "http://localhost:9200/_search?size=10&from=0&q=test",
			wantPreference: "_local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequest(http.MethodGet, tt.url, nil)
			require.NoError(t, err)

			rawBefore := req.URL.RawQuery

			injectPreference(req, preferenceLocal)

			// Check that preference has the expected value.
			got := req.URL.Query().Get(preferenceParam)
			require.Equal(t, tt.wantPreference, got, "unexpected preference value")

			if tt.wantNoChange {
				require.Equal(t, rawBefore, req.URL.RawQuery,
					"RawQuery should not be modified when preference already set")
			}

			if tt.wantRawQuery != "" {
				require.Equal(t, tt.wantRawQuery, req.URL.RawQuery)
			}

			// For cases with multiple existing params, verify all original params are preserved.
			if tt.name == "multiple existing params preserves all and adds preference" {
				q := req.URL.Query()
				require.Equal(t, "10", q.Get("size"), "size param should be preserved")
				require.Equal(t, "0", q.Get("from"), "from param should be preserved")
				require.Equal(t, "test", q.Get("q"), "q param should be preserved")
			}

			// For cases with existing params, verify the original param is still present.
			if tt.name == "existing params no preference appends preference=_local" {
				q := req.URL.Query()
				require.Equal(t, "10", q.Get("size"), "original param should be preserved")
			}
		})
	}
}
