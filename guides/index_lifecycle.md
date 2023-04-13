# Index Lifecycle

This guide covers OpenSearch Golang Client API actions for Index Lifecycle. You'll learn how to create, read, update, and delete indices in your OpenSearch cluster. We will also leverage index templates to create default settings and mappings for indices of certain patterns.

## Setup

In this guide, we will need an OpenSearch cluster with more than one node. Let's use the sample [docker-compose.yml](https://opensearch.org/samples/docker-compose.yml) to start a cluster with two nodes. The cluster's API will be available at `localhost:9200` with basic authentication enabled with default username and password of `admin:admin`.

To start the cluster, run the following command:

```bash
  cd /path/to/docker-compose.yml
  docker-compose up -d
```

Let's create a client instance to access this cluster:

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

## Index API Actions

### Create a new index

You can quickly create an index with default settings and mappings by using the `indices.create` API action. The following example creates an index named `paintings` with default settings and mappings:

```go
    paintingsIndex, err := client.Indices.Create("paintings")
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", paintingsIndex)
```

To specify settings and mappings, you can pass them as the `body` of the request. The following example creates an index named `movies` with custom settings and mappings:

```go
    movies := "movies"
    
    createMovieIndex, err := client.Indices.Create(movies,
    client.Indices.Create.WithBody(strings.NewReader(`{
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
        ),
    )
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", createMovieIndex)
```

When you create a new document for an index, OpenSearch will automatically create the index if it doesn't exist:

```go
    // return status code 404 Not Found
    res, err := client.Indices.Exists([]string{"burner"})
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
    
    res, err = client.Indices.Create(
        "burner",
        client.Indices.Create.WithBody(strings.NewReader(`{  "settings": {} }`)),
    )
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
    
    // return status code 200 OK
    res, err = client.Indices.Exists([]string{"burner"})
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

### Update an Index

You can update an index's settings and mappings by using the `indices.put_settings` and `indices.put_mapping` API actions.

The following example updates the `movies` index's number of replicas to `0`:

```go
    res, err := client.Indices.PutSettings(
        strings.NewReader(`{ "index": { "number_of_replicas": 0} }`),
        client.Indices.PutSettings.WithIndex(movies),
    )
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

The following example updates the `movies` index's mappings to add a new field named `director`:

```go
    res, err := client.Indices.PutMapping(
        strings.NewReader(`{ "properties": { "director": { "type": "text" } } }`),
        client.Indices.PutMapping.WithIndex(movies),
    )
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
```

### Get Metadata for an Index

Let's check if the index's settings and mappings have been updated by using the `indices.get` API action:

```go
    res, err := client.Indices.Get([]string{movies})
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", res)
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

Let's delete the `movies` index by using the `indices.delete` API action:

```go
    deleteIndexes, err := client.Indices.Delete([]string{movies})
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", deleteIndexes)
```

We can also delete multiple indices at once:

```go
    deleteIndexes, err := client.Indices.Delete(
        []string{movies, "burner", "paintings"},
        client.Indices.Delete.WithIgnoreUnavailable(true),
    )
    if err != nil {
        log.Printf("error occurred: [%s]", err.Error())
    }
    log.Printf("response: [%+v]", deleteIndexes)
```

Notice that we are passing `ignore unavailable` to the request. This tells the client to ignore the `404` error if the index doesn't exist for deletion. Without it, the above `delete` request will throw an error because the `movies` index has already been deleted in the previous example.

## Cleanup

All resources created in this guide are automatically deleted when the cluster is stopped. You can stop the cluster by running the following command:

```bash
  docker-compose down -v
```
