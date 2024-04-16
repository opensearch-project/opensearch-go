# Index Template

Index templates are a convenient way to define settings, mappings, and aliases for one or more indices when they are created. In this guide, you'll learn how to create an index template and apply it to an index.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
	"context"
	"crypto/tls"
	"fmt"
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
	// Initialize the client with SSL/TLS enabled.
	client, err := opensearchapi.NewClient(
		opensearchapi.Config{
			Client: opensearch.Config{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // For testing only. Use certificate for validation.
				},
				Addresses: []string{"https://localhost:9200"},
				Username:  "admin", // For testing only. Don't store credentials in code.
				Password:  "admin",
			},
		},
	)
	if err != nil {
		return err
	}

	ctx := context.Background()
```

## Index Template API Actions

### Create an Index Template

You can create an index template to define default settings and mappings for indices of certain patterns. The following example creates an index template named `books` with default settings and mappings for indices of the `books-*` pattern:

```go
	tempCreateResp, err := client.IndexTemplate.Create(
		ctx,
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: "books",
			Body: strings.NewReader(`{
    		"index_patterns": ["books-*"],
    		"template": {
    		  "settings": {
    		    "index": {
    		      "number_of_shards": 3,
    		      "number_of_replicas": 0
    		    }
    		  },
    		  "mappings": {
    		    "properties": {
    		      "title": { "type": "text" },
    		      "author": { "type": "text" },
    		      "published_on": { "type": "date" },
    		      "pages": { "type": "integer" }
    		    }
    		  }
    		},
				"priority": 50
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)
```

Now, when you create an index that matches the `books-*` pattern, OpenSearch will automatically apply the template's settings and mappings to the index. Let's create an index named `books-nonfiction` and verify that its settings and mappings match those of the template:

```go
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)

	createResp, err := client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "books-nonfiction"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	getResp, err := client.Indices.Get(ctx, opensearchapi.IndicesGetReq{Indices: []string{"books-nonfiction"}})
	if err != nil {
		return err
	}
	respAsJson, err := json.MarshalIndent(getResp.Indices, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", string(respAsJson))
```

### Multiple Index Templates

If multiple index templates match the index's name, OpenSearch will apply the template with the highest priority. The following example creates one more index templates named `books-fiction` with different settings:

```go
    // higher priority than the `books` template
	tempCreateResp, err = client.IndexTemplate.Create(
		ctx,
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: "books-fiction",
			Body: strings.NewReader(`{
    		"index_patterns": ["books-fiction-*"],
    		"template": {
    		  "settings": {
    		    "index": {
    		      "number_of_shards": 1,
    		      "number_of_replicas": 0
    		    }
    		  }
    		},
				"priority": 60
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)
```

When we create an index named `books-fiction-romance`, OpenSearch will apply the `books-fiction` template's settings to the index:

```go
	createResp, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "books-fiction-romance"})
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", createResp.Acknowledged)

	getResp, err = client.Indices.Get(ctx, opensearchapi.IndicesGetReq{Indices: []string{"books-fiction-romance"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp.Indices, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", string(respAsJson))
```

Let us clean up the created templates and indices:

```go
	delTempResp, err := client.IndexTemplate.Delete(ctx, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: "books*"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)

	delResp, err := client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"books-*"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted indices: %t\n", delResp.Acknowledged)
```

### Component Templates

Component templates are subsets of templates that can be used by index templates. This allows you do store duplicate index template parts in a Component template and reuse it across index templates. The following example creates a component template named `books` with default mappings and an index template with a `books-*` patterns referencing the component template:

```go
    // Component templates
	compTempCreateResp, err := client.ComponentTemplate.Create(
		ctx,
		opensearchapi.ComponentTemplateCreateReq{
			ComponentTemplate: "books",
			Body: strings.NewReader(`{
    		"template": {
    		  "mappings": {
    		    "properties": {
    		      "title": { "type": "text" },
    		      "author": { "type": "text" },
    		      "published_on": { "type": "date" },
    		      "pages": { "type": "integer" }
    		    }
    		  }
    		}
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Created: %t\n", compTempCreateResp.Acknowledged)

    // Index template composed of books component template
	tempCreateResp, err := client.IndexTemplate.Create(
		ctx,
		opensearchapi.IndexTemplateCreateReq{
			IndexTemplate: "books",
			Body: strings.NewReader(`{
    		"index_patterns": ["books-*"],
    		"template": {
    		  "settings": {
    		    "index": {
    		      "number_of_shards": 3,
    		      "number_of_replicas": 0
    		    }
    		  }
    		},
    		"composed_of": ["books"],
				"priority": 50
		}`),
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Index Tempalte created: %t\n", tempCreateResp.Acknowledged)
```

When we create an index named `books-fiction-horror`, OpenSearch will apply the `books` index template settings, and `books` component template mappings to the index:

```go
	createResp, err = client.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: "books-fiction-horror"})
	if err != nil {
		return err
	}
	fmt.Printf("Index created: %t\n", createResp.Acknowledged)

	getResp, err = client.Indices.Get(ctx, opensearchapi.IndicesGetReq{Indices: []string{"books-fiction-horror"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(getResp.Indices, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Document:\n%s\n", string(respAsJson))
```

### Get an Index Template

You can get an index template with the `IndexTemplate.Get()` action:

```go
	indexTempGetReq, err := client.IndexTemplate.Get(ctx, &opensearchapi.IndexTemplateGetReq{IndexTemplates: []string{"books"}})
	if err != nil {
		return err
	}
	respAsJson, err = json.MarshalIndent(indexTempGetReq, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("Get Index Template:\n%s\n", string(respAsJson))
```

### Delete an Index Template

You can delete an index template with the `IndexTemplate.Delete()` action:

```go
	delTempResp, err = client.IndexTemplate.Delete(ctx, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: "books*"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", delTempResp.Acknowledged)
```

## Cleanup

Let's delete all resources created in this guide:

```go
	delResp, err = client.Indices.Delete(
		ctx,
		opensearchapi.IndicesDeleteReq{
			Indices: []string{"books-*"},
			Params:  opensearchapi.IndicesDeleteParams{IgnoreUnavailable: opensearchapi.ToPointer(true)},
		},
	)
	if err != nil {
		return err
	}
	fmt.Printf("Deleted indices: %t\n", delResp.Acknowledged)

	compTempDelResp, err := client.ComponentTemplate.Delete(ctx, opensearchapi.ComponentTemplateDeleteReq{ComponentTemplate: "books*"})
	if err != nil {
		return err
	}
	fmt.Printf("Deleted templates: %t\n", compTempDelResp.Acknowledged)

	return nil
}
```
