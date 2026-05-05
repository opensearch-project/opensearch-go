- [Making Raw JSON REST Requests](#making-raw-json-rest-requests)
  - [Setup](#setup)
  - [Using Do for Typed Responses](#using-do-for-typed-responses)
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
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4"
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
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

## Using Do for Typed Responses

When you need to call an API that `opensearchapi` doesn't cover — plugin endpoints, newly released server APIs, or internal custom endpoints — use `opensearch.Do()` to execute a request and automatically unmarshal the JSON response into a struct.

The `Client.Do()` method accepts `any` for its response parameter, which means passing a non-pointer compiles but fails at runtime during JSON unmarshaling. The generic `opensearch.Do[T]()` function catches this mistake at compile time. `Client.Do()` is marked with a `Deprecated` doc annotation to steer callers toward the safer alternative — it remains fully functional and will not be removed, but `staticcheck` SA1019 will flag cross-package usage as a nudge.

First, define a request type that satisfies `opensearch.Request`:

```go
	// customReq wraps opensearch.BuildRequest to satisfy the opensearch.Request interface.
	type customReq struct {
		method string
		path   string
		body   io.Reader
	}

	func (r customReq) GetRequest() (*http.Request, error) {
		return opensearch.BuildRequest(r.method, r.path, r.body, nil, nil)
	}
```

Then use `opensearch.Do` to call the endpoint with a typed response:

```go
	type PluginStatusResp struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}

	ctx := context.Background()

	// Preferred: opensearch.Do[T] enforces *T at compile time.
	var pluginStatus PluginStatusResp
	req := customReq{method: http.MethodGet, path: "/_plugins/my_plugin/status"}
	resp, err := opensearch.Do(ctx, client.Client, req, &pluginStatus)
	if err != nil {
		return err
	}
	fmt.Printf("plugin status: %s (v%s), http: %d\n", pluginStatus.Status, pluginStatus.Version, resp.StatusCode)
```

If you pass a non-pointer value to `opensearch.Do`, the compiler rejects it:

```go
	// Compile error: cannot use pluginStatus (variable of type PluginStatusResp)
	// as *PluginStatusResp value in argument to opensearch.Do
	resp, err := opensearch.Do(ctx, client.Client, req, pluginStatus)
```

The three levels of the client API, from lowest to highest:

| Level | Function                                                           | Response handling                                         | When to use                                                |
| ----- | ------------------------------------------------------------------ | --------------------------------------------------------- | ---------------------------------------------------------- |
| Low   | `client.Perform(req)`                                              | Raw `*http.Response`; caller reads and closes body        | Proxying, streaming, full control needed                   |
| Mid   | `opensearch.Do(ctx, client, req, &resp)`                           | Automatic JSON unmarshal with compile-time pointer safety | Plugin APIs, unsupported endpoints, custom `Request` types |
| High  | `client.Search(ctx, req)` / `client.Indices.Create(ctx, req)` etc. | Fully typed request and response                          | Standard OpenSearch APIs                                   |

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
