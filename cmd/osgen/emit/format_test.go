// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
)

func TestLowerFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "standard", input: "Returns cluster health.", want: "returns cluster health."},
		{name: "already lower", input: "returns cluster health.", want: "returns cluster health."},
		{name: "acronym", input: "JSON body of the request.", want: "JSON body of the request."},
		{name: "empty", input: "", want: ""},
		{name: "single char", input: "A", want: "a"},
		{name: "single lower", input: "a", want: "a"},
		{name: "two upper (acronym)", input: "HTTP method.", want: "HTTP method."},
		{name: "unicode upper", input: "Étude", want: "étude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.LowerFirst(tt.input))
		})
	}
}

func TestSplitFirstLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantFirst string
		wantRest  string
	}{
		{name: "single line", input: "Hello world.", wantFirst: "Hello world.", wantRest: ""},
		{
			name:      "blank line separator",
			input:     "First paragraph.\n\nSecond paragraph.",
			wantFirst: "First paragraph.",
			wantRest:  "Second paragraph.",
		},
		{name: "newline no blank", input: "Line one.\nLine two.", wantFirst: "Line one.", wantRest: "Line two."},
		{name: "multiple paragraphs", input: "First.\n\nSecond.\n\nThird.", wantFirst: "First.", wantRest: "Second.\n\nThird."},
		{name: "trailing whitespace", input: "  First.  \n\n  Second.  ", wantFirst: "First.", wantRest: "Second."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotFirst, gotRest := emit.SplitFirstLine(tt.input)
			require.Equal(t, tt.wantFirst, gotFirst)
			require.Equal(t, tt.wantRest, gotRest)
		})
	}
}

func TestMethodComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   emit.MethodDocData
		checks []string
	}{
		{
			name: "full metadata",
			data: emit.MethodDocData{
				MethodName:      "GetRole",
				Group:           "security.get_role",
				Description:     "Retrieves one role.",
				HTTPMethods:     []string{http.MethodGet},
				PrimaryPath:     "/_plugins/_security/api/roles/{role}",
				VersionAdded:    "1.0.0",
				ExcludedDistros: []string{"amazon-managed", "amazon-serverless"},
				DocsURL:         "https://opensearch.org/docs/latest/security/access-control/api/#get-role",
			},
			checks: []string{
				"// GetRole retrieves one role.",
				"// GET /_plugins/_security/api/roles/{role}",
				"// Available: >= 1.0.0.",
				"// Not available on: amazon-managed, amazon-serverless.",
				"// See: https://opensearch.org/docs/latest/security/access-control/api/#get-role",
			},
		},
		{
			name: "no description fallback",
			data: emit.MethodDocData{
				MethodName:  "Health",
				Group:       "security.health",
				HTTPMethods: []string{http.MethodGet},
				PrimaryPath: "/_plugins/_security/health",
			},
			checks: []string{
				"// Health executes the security.health operation.",
				"// GET /_plugins/_security/health",
			},
		},
		{
			name: "multiple HTTP methods",
			data: emit.MethodDocData{
				MethodName:  "Search",
				Group:       "search",
				Description: "Returns results matching a query.",
				HTTPMethods: []string{http.MethodGet, http.MethodPost},
				PrimaryPath: "/{index}/_search",
			},
			checks: []string{
				"// Search returns results matching a query.",
				"// Path: /{index}/_search",
				"// Methods: GET, POST",
			},
		},
		{
			name: "deprecated operation",
			data: emit.MethodDocData{
				MethodName:        "OldGet",
				Group:             "old.get",
				Description:       "Fetches a resource.",
				HTTPMethods:       []string{http.MethodGet},
				PrimaryPath:       "/old/{id}",
				VersionAdded:      "1.0",
				VersionDeprecated: "2.0",
				DeprecationMsg:    "Use NewGet instead.",
			},
			checks: []string{
				"// OldGet fetches a resource.",
				"// GET /old/{id}",
				"// Deprecated: since 2.0.0. Available >= 1.0.0. Use NewGet instead.",
			},
		},
		{
			name: "minimal",
			data: emit.MethodDocData{
				MethodName: "Ping",
				Group:      "ping",
			},
			checks: []string{
				"// Ping executes the ping operation.",
			},
		},
		{
			name: "multi-line description",
			data: emit.MethodDocData{
				MethodName: "Create",
				Group:      "create",
				Description: "Creates a new document in the index.\n\n" +
					"Returns a 409 response when a document with a same ID already exists in the index.",
				HTTPMethods:  []string{http.MethodPut},
				PrimaryPath:  "/{index}/_create/{id}",
				VersionAdded: "1.0",
			},
			checks: []string{
				"// Create creates a new document in the index.",
				"// Returns a 409 response when a document with a same ID already exists in the index.",
				"// PUT /{index}/_create/{id}",
				"// Available: >= 1.0.0.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := emit.MethodComment(tt.data)
			for _, want := range tt.checks {
				require.Contains(t, got, want)
			}
		})
	}
}
