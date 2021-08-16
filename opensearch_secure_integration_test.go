// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// +build integration,secure

package opensearch_test

import (
	"crypto/tls"
	"encoding/json"
	"github.com/opensearch-project/opensearch-go"
	"github.com/stretchr/testify/assert"
	"log"
	"net/http"
	"testing"
)

func getSecuredClient() (*opensearch.Client, error){

	return opensearch.NewClient(opensearch.Config{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Addresses: []string{"https://localhost:9200"},
		Username: "admin",
		Password: "admin",
	})
}

type clusterVersion struct {
	Number      string `json:"number"`
	BuildFlavor string `json:"build_flavor"`
	Distribution string `json:"distribution"`
}

type Info struct {
	Version clusterVersion `json:"version"`
	Tagline string         `json:"tagline"`
}


func TestSecuredClientAPI(t *testing.T) {
	t.Run("Check Info", func(t *testing.T) {
		es, err := getSecuredClient()
		if err != nil {
			log.Fatalf("Error creating the client: %s\n", err)
		}
		res, err := es.Info()
		if err != nil {
			log.Fatalf("Error getting the response: %s\n", err)
		}
		defer res.Body.Close()

		var infoResponse Info
		err = json.NewDecoder(res.Body).Decode(&infoResponse)
		if err != nil {
			log.Fatalf("Error parsing the response: %s\n", err)
		}
		assert.True(t, len(infoResponse.Version.Number) >0, "version number should not be empty")
		assert.True(t, len(infoResponse.Tagline) >0, "tagline should not be empty")
		assert.True(t, len(infoResponse.Version.Distribution) >0 || len(infoResponse.Version.BuildFlavor) > 0,
			"Either distribution or build flavor should not be empty")
	})
}
