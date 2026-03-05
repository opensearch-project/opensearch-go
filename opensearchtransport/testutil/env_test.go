// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil_test

import (
	"crypto/tls"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// unsetEnv removes an environment variable for the duration of the test.
// Call AFTER t.Setenv for the same key so cleanup restores the original value.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	require.NoError(t, os.Unsetenv(key))
}

func TestIsSecure(t *testing.T) {
	tests := []struct {
		name     string
		envSet   bool
		envValue string
		want     bool
	}{
		{name: "unset defaults to false", envSet: false, want: false},
		{name: "true returns true", envSet: true, envValue: "true", want: true},
		{name: "false returns false", envSet: true, envValue: "false", want: false},
		{name: "garbage returns false", envSet: true, envValue: "notabool", want: false},
		{name: "1 returns true", envSet: true, envValue: "1", want: true},
		{name: "0 returns false", envSet: true, envValue: "0", want: false},
		{name: "empty string returns false", envSet: true, envValue: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envSet {
				t.Setenv("SECURE_INTEGRATION", tt.envValue)
			} else {
				t.Setenv("SECURE_INTEGRATION", "")
				unsetEnv(t, "SECURE_INTEGRATION")
			}
			got := testutil.IsSecure(t)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetScheme(t *testing.T) {
	t.Run("secure returns https", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "true")
		require.Equal(t, "https", testutil.GetScheme(t))
	})

	t.Run("insecure returns http", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "false")
		require.Equal(t, "http", testutil.GetScheme(t))
	})

	t.Run("unset returns http", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "")
		unsetEnv(t, "SECURE_INTEGRATION")
		require.Equal(t, "http", testutil.GetScheme(t))
	})
}

func TestGetPassword(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantPass string
	}{
		{name: "unset defaults to strong password", version: "", wantPass: "myStrongPassword123!"},
		{name: "latest uses strong password", version: "latest", wantPass: "myStrongPassword123!"},
		{name: "2.12.0 uses strong password", version: "2.12.0", wantPass: "myStrongPassword123!"},
		{name: "2.13.0 uses strong password", version: "2.13.0", wantPass: "myStrongPassword123!"},
		{name: "3.0.0 uses strong password", version: "3.0.0", wantPass: "myStrongPassword123!"},
		{name: "2.11.0 uses default password", version: "2.11.0", wantPass: "admin"},
		{name: "2.0.0 uses default password", version: "2.0.0", wantPass: "admin"},
		{name: "1.3.0 uses default password", version: "1.3.0", wantPass: "admin"},
		{name: "invalid version uses strong password", version: "not-a-version", wantPass: "myStrongPassword123!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OPENSEARCH_VERSION", tt.version)
			got := testutil.GetPassword(t)
			require.Equal(t, tt.wantPass, got)
		})
	}
}

func TestGetTestURL(t *testing.T) {
	t.Run("env var set returns parsed URL", func(t *testing.T) {
		t.Setenv("OPENSEARCH_URL", "https://custom-host:9201")
		t.Setenv("SECURE_INTEGRATION", "false")

		u := testutil.GetTestURL(t)
		require.Equal(t, "https", u.Scheme)
		require.Equal(t, "custom-host:9201", u.Host)
	})

	t.Run("env var unset secure returns https localhost", func(t *testing.T) {
		t.Setenv("OPENSEARCH_URL", "")
		unsetEnv(t, "OPENSEARCH_URL")
		t.Setenv("SECURE_INTEGRATION", "true")

		u := testutil.GetTestURL(t)
		require.Equal(t, "https", u.Scheme)
		require.Equal(t, "localhost:9200", u.Host)
	})

	t.Run("env var unset insecure returns http localhost", func(t *testing.T) {
		t.Setenv("OPENSEARCH_URL", "")
		unsetEnv(t, "OPENSEARCH_URL")
		t.Setenv("SECURE_INTEGRATION", "false")

		u := testutil.GetTestURL(t)
		require.Equal(t, "http", u.Scheme)
		require.Equal(t, "localhost:9200", u.Host)
	})

	t.Run("empty string falls back to default", func(t *testing.T) {
		t.Setenv("OPENSEARCH_URL", "")
		t.Setenv("SECURE_INTEGRATION", "false")

		u := testutil.GetTestURL(t)
		require.Equal(t, "http", u.Scheme)
		require.Equal(t, "localhost:9200", u.Host)
	})
}

func TestGetTestTransport(t *testing.T) {
	t.Run("secure returns TLS transport", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "true")

		tr := testutil.GetTestTransport(t)
		require.NotNil(t, tr)

		httpTr, ok := tr.(*http.Transport)
		require.True(t, ok, "expected *http.Transport, got %T", tr)
		require.NotNil(t, httpTr.TLSClientConfig)
		require.True(t, httpTr.TLSClientConfig.InsecureSkipVerify)
	})

	t.Run("insecure returns default transport", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "false")

		tr := testutil.GetTestTransport(t)
		require.Equal(t, http.DefaultTransport, tr)
	})

	t.Run("secure transport allows TLS negotiation", func(t *testing.T) {
		t.Setenv("SECURE_INTEGRATION", "true")

		tr := testutil.GetTestTransport(t)
		httpTr, ok := tr.(*http.Transport)
		require.True(t, ok)
		// MinVersion 0 means Go negotiates the highest mutually supported version.
		require.True(t, httpTr.TLSClientConfig.MinVersion == 0 || httpTr.TLSClientConfig.MinVersion >= tls.VersionTLS12,
			"MinVersion should be zero or >= TLS 1.2")
	})
}

func TestIsDebugEnabled(t *testing.T) {
	tests := []struct {
		name     string
		envSet   bool
		envValue string
		want     bool
	}{
		{name: "unset returns false", envSet: false, want: false},
		{name: "empty string returns true", envSet: true, envValue: "", want: true},
		{name: "true returns true", envSet: true, envValue: "true", want: true},
		{name: "false returns false", envSet: true, envValue: "false", want: false},
		{name: "1 returns true", envSet: true, envValue: "1", want: true},
		{name: "garbage returns false", envSet: true, envValue: "notabool", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envSet {
				t.Setenv("OPENSEARCH_GO_DEBUG", tt.envValue)
			} else {
				t.Setenv("OPENSEARCH_GO_DEBUG", "")
				unsetEnv(t, "OPENSEARCH_GO_DEBUG")
			}
			got := testutil.IsDebugEnabled(t)
			require.Equal(t, tt.want, got)
		})
	}
}
