// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v3"
	"github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	// Initialize the client with SSL/TLS enabled.
	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // For testing only. Use certificate for validation.
				},
				Addresses: []string{"https://localhost:9200"},
				Username:  "admin", // For testing only. Don't store credentials in code.
				Password:  "myStrongPassword123!",
			},
		},
	)
	if err != nil {
		return err
	}

	ctx := context.Background()

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "paintings"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	createResp, err = client.Indices.Create(
		ctx,
		opensearchapi.IndicesCreateReq{
			Index: "movies",
			Body: strings.NewReader(`{
				"settings": {
				    "index": {
				        "number_of_shards": 2,
				        "number_of_replicas": 1
				    }
				},
				"mappings": {
				    "properties": {
				        "title": {
				            "type": "text"
				        },
				        "year": {
				            "type": "integer"
				        }
				    }
				}
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	_, err = client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{"burner"}})
	fmt.Printf("%s\n", err)

	indexResp, err := client.Index(ctx, opensearchapi.IndexReq{Index: "burner", Body: strings.NewReader(`{"foo": "bar"}`)})
	if err != nil {
		return err
	}
	fmt.Printf("Index: %s\n", indexResp.Result)

	existsResp, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{"burner"}})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", existsResp)

	settingsPutResp, err := client.Indices.Settings.Put(
		ctx,
		opensearchapi.SettingsPutReq{
			Indices: []string{"burner"},
			Body:    strings.NewReader(`{"index":{"number_of_replicas":0}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Settings updated: %t\n", settingsPutResp.Acknowledged)

	mappingPutResp, err := client.Indices.Mapping.Put(
		ctx,
		opensearchapi.MappingPutReq{
			Indices: []string{"movies"},
			Body:    strings.NewReader(`{"properties":{ "director":{"type":"text"}}}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Mappings updated: %t\n", mappingPutResp.Acknowledged)

	getResp, err := client.Indices.Get(
		ctx,
		opensearchapi.IndicesGetReq{
			Indices: []string{"movies"},
		},
	)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(getResp.Indices, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", string(respAsJson))

	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"movies", "paintings", "burner"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)

	return nil
}
