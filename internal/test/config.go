// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ostest

import (
	"crypto/tls"
	"net/http"
	"os"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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

// GetPassword returns the password suited for the opensearch version
func GetPassword() (string, error) {
	var (
		major, minor int64
		err          error
	)
	password := "admin"
	version := os.Getenv("OPENSEARCH_VERSION")

	if version != "latest" && version != "" {
		major, minor, _, err = opensearch.ParseVersion(version)
		if err != nil {
			return "", err
		}
		if version == "latest" || major > 2 || (major == 2 && minor >= 12) {
			password = "myStrongPassword123!"
		}
	} else {
		password = "myStrongPassword123!"
	}
	return password, nil
}
