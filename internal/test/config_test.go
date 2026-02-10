// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build !integration

package ostest_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ostest "github.com/opensearch-project/opensearch-go/v4/internal/test"
)

const (
	// Test password constants matching internal/test/config.go
	defaultPasswordPre212  = "admin"
	defaultPasswordPost212 = "myStrongPassword123!"
)

func TestIsSecure(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		want     bool
	}{
		{
			name:     "returns true when SECURE_INTEGRATION is true",
			envValue: "true",
			want:     true,
		},
		{
			name:     "returns false when SECURE_INTEGRATION is false",
			envValue: "false",
			want:     false,
		},
		{
			name:     "returns false when SECURE_INTEGRATION is empty",
			envValue: "",
			want:     false,
		},
		{
			name:     "returns false when SECURE_INTEGRATION is invalid",
			envValue: "invalid",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalValue := os.Getenv("SECURE_INTEGRATION")
			defer func() {
				if originalValue != "" {
					os.Setenv("SECURE_INTEGRATION", originalValue)
				} else {
					os.Unsetenv("SECURE_INTEGRATION")
				}
			}()

			// Set test env value
			if tt.envValue != "" {
				os.Setenv("SECURE_INTEGRATION", tt.envValue)
			} else {
				os.Unsetenv("SECURE_INTEGRATION")
			}

			got := ostest.IsSecure()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetPassword(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "returns pre-2.12 password for empty version",
			version: "",
			want:    defaultPasswordPre212,
			wantErr: false,
		},
		{
			name:    "returns post-2.12 password for latest version",
			version: "latest",
			want:    defaultPasswordPost212,
			wantErr: false,
		},
		{
			name:    "returns pre-2.12 password for version 2.11.0",
			version: "2.11.0",
			want:    defaultPasswordPre212,
			wantErr: false,
		},
		{
			name:    "returns post-2.12 password for version 2.12.0",
			version: "2.12.0",
			want:    defaultPasswordPost212,
			wantErr: false,
		},
		{
			name:    "returns post-2.12 password for version 2.13.0",
			version: "2.13.0",
			want:    defaultPasswordPost212,
			wantErr: false,
		},
		{
			name:    "returns pre-2.12 password for version 1.3.0",
			version: "1.3.0",
			want:    defaultPasswordPre212,
			wantErr: false,
		},
		{
			name:    "handles version with v prefix",
			version: "v2.12.0",
			want:    defaultPasswordPost212,
			wantErr: false,
		},
		{
			name:    "returns error for invalid version format",
			version: "invalid",
			want:    "",
			wantErr: true,
		},
		{
			name:    "returns error for malformed version",
			version: "2.x.y",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalValue := os.Getenv("OPENSEARCH_VERSION")
			defer func() {
				if originalValue != "" {
					os.Setenv("OPENSEARCH_VERSION", originalValue)
				} else {
					os.Unsetenv("OPENSEARCH_VERSION")
				}
			}()

			// Set test env value
			if tt.version != "" {
				os.Setenv("OPENSEARCH_VERSION", tt.version)
			} else {
				os.Unsetenv("OPENSEARCH_VERSION")
			}

			got, err := ostest.GetPassword()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "invalid version format")
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestGetPasswordForCluster(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		wantList []string
	}{
		{
			name:     "returns pre-2.12 first for empty version",
			version:  "",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
		{
			name:     "returns pre-2.12 first for latest version",
			version:  "latest",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
		{
			name:     "returns pre-2.12 first for version 2.11.0",
			version:  "2.11.0",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
		{
			name:     "returns post-2.12 first for version 2.12.0",
			version:  "2.12.0",
			wantList: []string{defaultPasswordPost212, defaultPasswordPre212},
		},
		{
			name:     "returns post-2.12 first for version 2.13.0",
			version:  "2.13.0",
			wantList: []string{defaultPasswordPost212, defaultPasswordPre212},
		},
		{
			name:     "returns pre-2.12 first for version 1.3.0",
			version:  "1.3.0",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
		{
			name:     "handles version with v prefix",
			version:  "v2.12.0",
			wantList: []string{defaultPasswordPost212, defaultPasswordPre212},
		},
		{
			name:     "returns pre-2.12 first for invalid version",
			version:  "invalid",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
		{
			name:     "returns pre-2.12 first for malformed version",
			version:  "2.x.y",
			wantList: []string{defaultPasswordPre212, defaultPasswordPost212},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalValue := os.Getenv("OPENSEARCH_VERSION")
			defer func() {
				if originalValue != "" {
					os.Setenv("OPENSEARCH_VERSION", originalValue)
				} else {
					os.Unsetenv("OPENSEARCH_VERSION")
				}
			}()

			// Set test env value
			if tt.version != "" {
				os.Setenv("OPENSEARCH_VERSION", tt.version)
			} else {
				os.Unsetenv("OPENSEARCH_VERSION")
			}

			got := ostest.GetPasswordForCluster()
			require.Len(t, got, 2)
			assert.Equal(t, tt.wantList, got)
		})
	}
}

func TestClientConfig(t *testing.T) {
	t.Run("insecure config", func(t *testing.T) {
		// Save original env value
		originalValue := os.Getenv("SECURE_INTEGRATION")
		defer func() {
			if originalValue != "" {
				os.Setenv("SECURE_INTEGRATION", originalValue)
			} else {
				os.Unsetenv("SECURE_INTEGRATION")
			}
		}()

		os.Setenv("SECURE_INTEGRATION", "false")

		cfg, err := ostest.ClientConfig()
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, []string{"http://localhost:9200"}, cfg.Client.Addresses)
		assert.Empty(t, cfg.Client.Username)
		assert.Empty(t, cfg.Client.Password)
	})

	t.Run("secure config with version 2.11.0", func(t *testing.T) {
		// Save original env values
		originalSecure := os.Getenv("SECURE_INTEGRATION")
		originalVersion := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			if originalSecure != "" {
				os.Setenv("SECURE_INTEGRATION", originalSecure)
			} else {
				os.Unsetenv("SECURE_INTEGRATION")
			}
			if originalVersion != "" {
				os.Setenv("OPENSEARCH_VERSION", originalVersion)
			} else {
				os.Unsetenv("OPENSEARCH_VERSION")
			}
		}()

		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "2.11.0")

		cfg, err := ostest.ClientConfig()
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, []string{"https://localhost:9200"}, cfg.Client.Addresses)
		assert.Equal(t, "admin", cfg.Client.Username)
		assert.Equal(t, defaultPasswordPre212, cfg.Client.Password)
		assert.NotNil(t, cfg.Client.Transport)
	})

	t.Run("secure config with version 2.12.0", func(t *testing.T) {
		// Save original env values
		originalSecure := os.Getenv("SECURE_INTEGRATION")
		originalVersion := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			if originalSecure != "" {
				os.Setenv("SECURE_INTEGRATION", originalSecure)
			} else {
				os.Unsetenv("SECURE_INTEGRATION")
			}
			if originalVersion != "" {
				os.Setenv("OPENSEARCH_VERSION", originalVersion)
			} else {
				os.Unsetenv("OPENSEARCH_VERSION")
			}
		}()

		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "2.12.0")

		cfg, err := ostest.ClientConfig()
		require.NoError(t, err)
		require.NotNil(t, cfg)
		assert.Equal(t, []string{"https://localhost:9200"}, cfg.Client.Addresses)
		assert.Equal(t, "admin", cfg.Client.Username)
		assert.Equal(t, defaultPasswordPost212, cfg.Client.Password)
		assert.NotNil(t, cfg.Client.Transport)
	})

	t.Run("secure config with invalid version", func(t *testing.T) {
		// Save original env values
		originalSecure := os.Getenv("SECURE_INTEGRATION")
		originalVersion := os.Getenv("OPENSEARCH_VERSION")
		defer func() {
			if originalSecure != "" {
				os.Setenv("SECURE_INTEGRATION", originalSecure)
			} else {
				os.Unsetenv("SECURE_INTEGRATION")
			}
			if originalVersion != "" {
				os.Setenv("OPENSEARCH_VERSION", originalVersion)
			} else {
				os.Unsetenv("OPENSEARCH_VERSION")
			}
		}()

		os.Setenv("SECURE_INTEGRATION", "true")
		os.Setenv("OPENSEARCH_VERSION", "invalid")

		cfg, err := ostest.ClientConfig()
		require.Error(t, err)
		assert.Nil(t, cfg)
		assert.Contains(t, err.Error(), "invalid version format")
	})
}
