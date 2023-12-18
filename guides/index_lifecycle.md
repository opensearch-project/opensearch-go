# Index Lifecycle

This guide covers OpenSearch Golang Client API actions for Index Lifecycle. You'll learn how to create, get, update settings, update mapping, and delete indices in your OpenSearch cluster. We will also leverage index templates to create default settings and mappings for indices of certain patterns.

## Setup

In this guide, we will need an OpenSearch cluster with more than one node. Let's use the sample [docker-compose.yml](https://opensearch.org/samples/docker-compose.yml) to start a cluster with two nodes. The cluster's API will be available at `localhost:9200` with basic authentication enabled with default username and password of `admin:< Admin password >`.

To start the cluster, run the following command:

```bash
  cd /path/to/docker-compose.yml
  docker-compose up -d
```

Let's create a client instance to access this cluster:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

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
				Password:  "< Admin password >",
			},
		},
	)
	if err != nil {
		return err
	}
	ctx := context.Background()
```

## Index API Actions

### Create a new index

You can quickly create an index with default settings and mappings by using the `client.Indices.Create` action. The following example creates an index named `paintings` with default settings and mappings:

```go
	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "paintings"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)
```

To specify settings and mappings, you can pass them as the `body` of the request. The following example creates an index named `movies` with custom settings and mappings:

```go
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
```

When you create a new document for an index, OpenSearch will automatically create the index if it doesn't exist:

```go
    // return status code 404 Not Found
	existsResp, err := client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{"burner"}})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", existsResp)

	indexResp, err := client.Index(ctx, opensearchapi.IndexReq{Index: "burner", Body: strings.NewReader(`{"foo": "bar"}`)})
	if err != nil {
		return err
	}
	fmt.Printf("Index: %s\n", indexResp.Result)

    // return status code 200 OK
	existsResp, err = client.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{"burner"}})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", existsResp)
```

### Update an Index

You can update an index's settings and mappings by using the `client.Indices.Settings.Put()` and `client.Indices.Mapping.Put()` actions.

The following example updates the `movies` index's number of replicas to `0`:

```go
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
```

The following example updates the `movies` index's mappings to add a new field named `director`:

```go
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
```

### Get Metadata for an Index

Let's check if the index's settings and mappings have been updated by using the `client.Indices.Get()` action:

```go
	getResp, err := client.Indices.Get(
		ctx,
		opensearchapi.IndicesGetReq{
			Indices: []string{"movies"},
		},
	)
	if err != nil {
		return err
	}
    // Json Marshal the struct to pretty print
	respAsJson, err := json.MarshalIndent(getResp.Indices, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", string(respAsJson))
```

The response body contains the index's settings and mappings:

```json
{
  "movies": {
    "aliases": {},
    "mappings": {
      "properties": {
        "director": {
          "type": "text"
        },
        "title": {
          "type": "text"
        },
        "year": {
          "type": "integer"
        }
      }
    },
    "settings": {
      "index": {
        "creation_date": "1681033762803",
        "number_of_shards": "2",
        "number_of_replicas": "1",
        "uuid": "n9suHX2wTPG3Mq2y-3Lvdw",
        "version": {
          "created": "136277827"
        },
        "provided_name": "movies"
      }
    }
  }
}
```

### Delete an Index

Let's delete the `movies` index by using the `client.Indices.Delete()` action:

```go
	delResp, err := client.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{Indices: []string{"movies"}})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted: %t\n", delResp.Acknowledged)
```

We can also delete multiple indices at once:

```go
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
```

Notice that we are passing `ignore unavailable` to the request. This tells the server to ignore the `404` error if the index doesn't exist for deletion. Without it, the above `delete` request will throw an error because the `movies` index has already been deleted in the previous example.

## Cleanup

All resources created in this guide are automatically deleted when the cluster is stopped. You can stop the cluster by running the following command:

```bash
  docker-compose down -v
```
