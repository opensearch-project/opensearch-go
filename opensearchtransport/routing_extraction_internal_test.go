// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/http"
	"net/url"
	"testing"
)

func TestExtractRouting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rawQuery string
		want     string
	}{
		{name: "empty query", rawQuery: "", want: ""},
		{name: "no routing param", rawQuery: "pretty=true&timeout=5s", want: ""},
		{name: "routing only param", rawQuery: "routing=abc123", want: "abc123"},
		{name: "routing first", rawQuery: "routing=user42&pretty=true", want: "user42"},
		{name: "routing last", rawQuery: "pretty=true&routing=user42", want: "user42"},
		{name: "routing middle", rawQuery: "pretty=true&routing=user42&timeout=5s", want: "user42"},
		{name: "routing empty value", rawQuery: "routing=", want: ""},
		{name: "routing empty value with other", rawQuery: "routing=&pretty=true", want: ""},
		{name: "false positive _routing", rawQuery: "_routing=abc123", want: ""},
		{name: "false positive my_routing", rawQuery: "my_routing=abc123", want: ""},
		{name: "false positive routingx", rawQuery: "routingx=abc123", want: ""},
		{name: "percent encoded space", rawQuery: "routing=foo%20bar", want: "foo bar"},
		{name: "percent encoded slash", rawQuery: "routing=a%2Fb", want: "a/b"},
		{name: "numeric routing", rawQuery: "routing=12345", want: "12345"},
		{name: "routing with other params", rawQuery: "routing=abc&size=10", want: "abc"},
		{name: "question mark prefix stripped", rawQuery: "?routing=abc", want: "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := &http.Request{URL: mustParseURL("http://localhost:9200/myindex/_search?" + tt.rawQuery)}
			// Override RawQuery to ensure we use exactly the test value
			// (mustParseURL may normalize).
			req.URL.RawQuery = tt.rawQuery

			got := extractRouting(req)
			if got != tt.want {
				t.Errorf("extractRouting() = %q, want %q", got, tt.want)
			}
		})
	}
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
