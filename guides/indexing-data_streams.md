# Data Streams API

> **Runnable example:** [`_samples/indexing-data_stream.go`](../_samples/indexing-data_stream.go)

> **Note:** Examples in this guide use raw JSON strings for request bodies because the `opensearchapi` package accepts `io.Reader`. When building bodies from user-supplied values, always use `opensearchutil.NewJSONReader` with a Go struct or map instead of string interpolation. See [Security](config-security.md#request-body-construction) for details.

## Setup

First, create a client instance with the following code:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	client, err := opensearchapi.NewDefaultClient()
	if err != nil {
		return err
	}

	ctx := context.Background()
```

Next, create an index template with a data_stream section:

```go
	tempCreateResp, err := client.Indices.PutIndexTemplate(
		ctx,
		opensearchapi.IndicesPutIndexTemplateReq{
			Name: "books",
			BodyReader: strings.NewReader(`{
    		    "index_patterns": ["books-nonfiction"],
    		    "template": {
    		      "settings": {
    		        "index": {
    		          "number_of_shards": 3,
    		          "number_of_replicas": 0
    		        }
    		      }
    		    },
				"data_stream": {},
				"priority": 50
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)
```

## Create Data Streams

The `Indices.CreateDataStream()` action allows you to create a new Data Stream:

```go
	createResp, err := client.Indices.CreateDataStream(ctx, opensearchapi.IndicesCreateDataStreamReq{Name: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)
```

## Get Data Streams

The `Indices.GetDataStream()` action allows you to get information about Data Streams. Omitting the Request struct will get all DataStreams:

```go
	getResp, err := client.Indices.GetDataStream(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))
```

By specifying a Data Stream in the request you'll only see the requested Data Stream:

```go
	getResp, err = client.Indices.GetDataStream(ctx, &opensearchapi.IndicesGetDataStreamReq{Name: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get DataStream:\n%s\n", string(respAsJson))
```

## Get Data Stream Stats

The `Indices.DataStreamsStats()` action allows you to get stats about Data Streams:

```go
	statsResp, err := client.Indices.DataStreamsStats(ctx, nil)
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(statsResp, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Stats DataStream:\n%s\n", string(respAsJson))
```

## Delete Data Streams

The `Indices.DeleteDataStream()` action allows you to delete a Data Stream:

```go
	delResp, err := client.Indices.DeleteDataStream(ctx, &opensearchapi.IndicesDeleteDataStreamReq{Name: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	fmt.Printf("DataStream deleted: %t\n", delResp.Acknowledged)
```

## Cleanup

To clean up the resources created in this guide, delete the index template:

```go
	delTempResp, err := client.Indices.DeleteIndexTemplate(ctx, opensearchapi.IndicesDeleteIndexTemplateReq{Name: "books"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)

	return nil
}
```
