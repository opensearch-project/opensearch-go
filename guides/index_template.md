# Index Template

Index templates are a convenient way to define settings, mappings, and aliases for one or more indices when they are created. In this guide, you'll learn how to create an index template and apply it to an index.

## Setup

Assuming you have OpenSearch running locally on port 9200, you can create a client instance with the following code:

```go
package main

import (
    "github.com/opensearch-project/opensearch-go/v2"
    "log"
)

func main() {
    client, err := opensearch.NewDefaultClient()
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", client)
}
```

## Index Template API Actions

### Create an Index Template

You can create an index template to define default settings and mappings for indices of certain patterns. The following example creates an index template named `books` with default settings and mappings for indices of the `books-*` pattern:

```go
body := strings.NewReader(`{
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
    }
}`)

res, err := client.Indices.PutIndexTemplate("books", body)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

Now, when you create an index that matches the `books-*` pattern, OpenSearch will automatically apply the template's settings and mappings to the index. Let's create an index named `books-nonfiction` and verify that its settings and mappings match those of the template:

```go
res, err = client.Indices.Create("books-nonfiction")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// check mappings properties
res, err = client.Indices.Get([]string{"books-nonfiction"})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Multiple Index Templates

If multiple index templates match the index's name, OpenSearch will apply the template with the highest priority. The following example creates two index templates named `books-*` and `books-fiction-*` with different settings:

```go
res, err := client.Indices.PutIndexTemplate("books", strings.NewReader(`{
    "index_patterns": ["books-*"],
    "priority": 0,
    "template": {
      "settings": {
        "index": {
          "number_of_shards": 3,
          "number_of_replicas": 0
        }
      }
    }
}`))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// higher priority than the `books` template
res, err = client.Indices.PutIndexTemplate("books-fiction", strings.NewReader(`{
    "index_patterns": ["books-fiction-*"],
    "priority": 1,
    "template": {
      "settings": {
        "index": {
          "number_of_shards": 1,
          "number_of_replicas": 1
        }
      }
    }
}`))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

When we create an index named `books-fiction-romance`, OpenSearch will apply the `books-fiction-*` template's settings to the index:

```go
res, err = client.Indices.Create("books-fiction-romance")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.Get([]string{"books-fiction-romance"})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Composable Index Templates

Composable index templates are a new type of index template that allow you to define multiple component templates and compose them into a final template. The following example creates a component template named `books_mappings` with default mappings for indices of the `books-*` and `books-fiction-*` patterns:

```go
// delete index templates if they exist
res, err := client.Indices.DeleteIndexTemplate("books-*")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// delete indices if they exist
res, err = client.Indices.Delete([]string{"books-*", "books-fiction-*"})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// Composable Index Templates
res, err = client.Cluster.PutComponentTemplate("books_mappings", strings.NewReader(`{
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
}`))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// use the `books_mappings` component template with priority 0
res, err = client.Indices.PutIndexTemplate("books", strings.NewReader(`{
    "index_patterns": ["books-*"],
    "composed_of": ["books_mappings"],
    "priority": 0,
    "template": {
      "settings": {
        "index": {
          "number_of_shards": 3,
          "number_of_replicas": 0
        }
      }
    }
}`))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

// use the `books_mappings` component template with priority 1
res, err = client.Indices.PutIndexTemplate("books", strings.NewReader(`{
    "index_patterns": ["books-fiction-*"],
    "composed_of": ["books_mappings"],
    "priority": 1,
    "template": {
      "settings": {
        "index": {
          "number_of_shards": 3,
          "number_of_replicas": 0
        }
      }
    }
}`))
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

When we create an index named `books-fiction-horror`, OpenSearch will apply the `books-fiction-*` template's settings, and `books_mappings` template mappings to the index:

```go
res, err = client.Indices.Create("books-fiction-horror")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)

res, err = client.Indices.Get([]string{"books-fiction-horror"})
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Get an Index Template

You can get an index template with the `get_index_template` API action:

```go
res, err = client.Indices.GetIndexTemplate(
    client.Indices.GetIndexTemplate.WithName("books"),
)
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

### Delete an Index Template

You can delete an index template with the `delete_template` API action:

```go
res, err = client.Indices.DeleteIndexTemplate("books")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```

## Cleanup

Let's delete all resources created in this guide:

```go
res, err = client.Indices.DeleteIndexTemplate("books-fiction")
if err != nil {
    log.Printf("error occurred: [%s]", err.Error())
}
log.Printf("response: [%+v]", res)
```
