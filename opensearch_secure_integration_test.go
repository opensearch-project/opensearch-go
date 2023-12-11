// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build integration && secure

package opensearch_test

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

func getSecuredClient() (*opensearchapi.Client, error) {
	return opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				Username:  "admin",
				Password:  "admin",
				Addresses: []string{"https://localhost:9200"},
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			},
		},
	)
}

type clusterVersion struct {
	Number       string `json:"number"`
	BuildFlavor  string `json:"build_flavor"`
	Distribution string `json:"distribution"`
}

type Info struct {
	Version clusterVersion `json:"version"`
	Tagline string         `json:"tagline"`
}

func TestSecuredClientAPI(t *testing.T) {
	t.Run("Check Info", func(t *testing.T) {
		ctx := context.Background()
		client, err := getSecuredClient()
		if err != nil {
			log.Fatalf("Error creating the client: %s\n", err)
		}
		res, err := client.Info(ctx, nil)
		if err != nil {
			log.Fatalf("Error getting the response: %s\n", err)
		}

		assert.True(t, len(res.Version.Number) > 0, "version number should not be empty")
		assert.True(t, len(res.Tagline) > 0, "tagline should not be empty")
		assert.True(t, len(res.Version.Distribution) > 0, "distribution should not be empty")
	})
}
