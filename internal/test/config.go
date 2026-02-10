// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ostest

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/mod/semver"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

const (
	// OpenSearch default admin passwords
	defaultPasswordPre212  = "admin"                // Default admin password in OpenSearch < 2.12.0
	defaultPasswordPost212 = "myStrongPassword123!" // Default admin password in OpenSearch >= 2.12.0

	// Version where default admin password changed
	defaultPasswordChangeVersion = "v2.12.0"
)

// IsSecure returns true when SECURE_INTEGRATION env is set to true
func IsSecure() bool {
	return os.Getenv("SECURE_INTEGRATION") == "true"
}

// ClientConfig returns an opensearchapi.Config for both secure and insecure opensearch
func ClientConfig() (*opensearchapi.Config, error) {
	if !IsSecure() {
		// For insecure integration tests, explicitly use HTTP
		return &opensearchapi.Config{
			Client: opensearch.Config{
				Addresses: []string{"http://localhost:9200"},
			},
		}, nil
	}

	password, err := GetPassword()
	if err != nil {
		return nil, err
	}

	return &opensearchapi.Config{
		Client: opensearch.Config{
			Username:  "admin",
			Password:  password,
			Addresses: []string{"https://localhost:9200"},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
	}, nil
}

// GetPassword returns the admin password for the opensearch version.
// OpenSearch 2.12.0+ changed the default admin password from "admin" to "myStrongPassword123!".
//
// Note: This function tries to determine the correct password based on OPENSEARCH_VERSION env var.
// If the env var doesn't match the actual running cluster, authentication may fail.
func GetPassword() (string, error) {
	version := os.Getenv("OPENSEARCH_VERSION")

	// Default to pre-2.12 password for empty version or versions < 2.12.0
	password := defaultPasswordPre212

	if version == "latest" {
		// Latest uses the post-2.12 default password
		password = defaultPasswordPost212
	} else if version != "" {
		// Normalize version to semver format (v2.12.0)
		if version[0] != 'v' {
			version = "v" + version
		}

		// Validate semver format
		if !semver.IsValid(version) {
			return "", fmt.Errorf("invalid version format: %s", version)
		}

		// OpenSearch 2.12.0+ uses the new default password
		if semver.Compare(version, defaultPasswordChangeVersion) >= 0 {
			password = defaultPasswordPost212
		}
	}

	return password, nil
}

// GetPasswordForCluster returns the admin password by trying to detect the actual cluster version.
// This is more reliable than GetPassword() when OPENSEARCH_VERSION env var might not match reality.
// It returns both possible passwords to try in order.
func GetPasswordForCluster() []string {
	version := os.Getenv("OPENSEARCH_VERSION")

	// If version is set and valid, trust it
	if version != "" && version != "latest" {
		if version[0] != 'v' {
			version = "v" + version
		}
		if semver.IsValid(version) {
			if semver.Compare(version, defaultPasswordChangeVersion) >= 0 {
				// For 2.12+, try post-2.12 password first, then fallback to pre-2.12
				return []string{defaultPasswordPost212, defaultPasswordPre212}
			}
			// For < 2.12, try pre-2.12 password first, then fallback to post-2.12
			return []string{defaultPasswordPre212, defaultPasswordPost212}
		}
	}

	// Unknown or latest version: try pre-2.12 password first (most common), then post-2.12
	return []string{defaultPasswordPre212, defaultPasswordPost212}
}
