- [Making Raw JSON REST Requests](#making-raw-json-rest-requests)
  - [Setup](#setup)
  - [GET](#get)
  - [PUT](#put)
  - [POST](#post)
  - [DELETE](#delete)

# Making Raw JSON REST Requests

The OpenSearch client implements many high-level REST DSLs that invoke OpenSearch APIs. However you may find yourself in a situation that requires you to invoke an API that is not supported by the client. Use `client.Perform` to do so.

## Setup

Let's create a client instance:

```go
package main

import (
	"fmt"
	"io"
	"net/http"
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
	client, err := opensearchapi.NewDefaultClient()
	if err != nil {
		return err
	}
```

## GET

The following example returns the server version information via `GET /`.

```go
	infoRequest, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		return err
	}

	infoResponse, err := client.Client.Perform(infoRequest)
	if err != nil {
		return err
	}

	resBody, err := io.ReadAll(infoResponse.Body)
	if err != nil {
		return err
	}
	fmt.Printf("client info: %s\n", resBody)
```

## PUT

The following example creates an index.

```go
	var index_body = strings.NewReader(`{
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
}`)

	createIndexRequest, err := http.NewRequest("PUT", "/movies", index_body)
	if err != nil {
		return err
	}
	createIndexRequest.Header["Content-Type"] = []string{"application/json"}
	createIndexResp, err := client.Client.Perform(createIndexRequest)
	if err != nil {
		return err
	}
	createIndexRespBody, err := io.ReadAll(createIndexResp.Body)
	if err != nil {
		return err
	}
	fmt.Println("create index: ", string(createIndexRespBody))
```

Note that the client will raise errors automatically. For example, if the index already exists, an error containing `resource_already_exists_exception` root cause will be thrown.

## POST

The following example searches for a document.

```go
	query := strings.NewReader(`{
    "size": 5,
    "query": {
        "multi_match": {
        "query": "miller",
        "fields": ["title^2", "director"]
        }
    }
}`)
	searchRequest, err := http.NewRequest("POST", "/movies/_search", query)
	if err != nil {
		return err
	}
	searchRequest.Header["Content-Type"] = []string{"application/json"}
	searchResp, err := client.Client.Perform(searchRequest)
	if err != nil {
		return err
	}
	searchRespBody, err := io.ReadAll(searchResp.Body)
	if err != nil {
		return err
	}
	fmt.Println("search: ", string(searchRespBody))
```

## DELETE

The following example deletes an index.

```go
	deleteIndexRequest, err := http.NewRequest("DELETE", "/movies", nil)
	if err != nil {
		return err
	}
	deleteIndexResp, err := client.Client.Perform(deleteIndexRequest)
	if err != nil {
		return err
	}
	deleteIndexRespBody, err := io.ReadAll(deleteIndexResp.Body)
	if err != nil {
		return err
	}
	fmt.Println("delete index: ", string(deleteIndexRespBody))
	return nil
}
```
