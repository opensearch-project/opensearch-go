# Data Streams API

## Setup

First, create a client instance with the following code:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
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
	tempCreateResp, err := client.IndexTemplate.Create(
		ctx,
		opensearchapi.IndexTemplateCreateReq{
		    IndexTemplate: "books",
			Body: strings.NewReader(`{
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

The `DataStream.Create()` action allows you to create a new Data Stream:

```go
	createResp, err := client.DataStream.Create(ctx, opensearchapi.DataStreamCreateReq{DataStream: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)
```

## Get Data Streams

The `DataStream.Get()` action allows you to get information about Data Streams. Omitting the Request struct will get all DataStreams:

```go
	getResp, err := client.DataStream.Get(ctx, nil)
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
	getResp, err = client.DataStream.Get(ctx, &opensearchapi.DataStreamGetReq{DataStreams: []string{"books-nonfiction"}})
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

The `DataStream.Stats()` action allows you to get stats about Data Streams:

```go
	statsResp, err := client.DataStream.Stats(ctx, nil)
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

The `DataStream.Delete()` action allows you to delete a Data Stream:

```go
	delResp, err := client.DataStream.Delete(ctx, opensearchapi.DataStreamDeleteReq{DataStream: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("DataStream deleted: %t\n", delResp.Acknowledged)
```

## Cleanup

To clean up the resources created in this guide, delete the index template:

```go
	delTempResp, err := client.IndexTemplate.Delete(ctx, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: "books"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)

	return nil
}
```
