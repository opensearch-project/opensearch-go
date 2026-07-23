# Upgrading to >= 3.0.0

Version 3.0.0 is a major refactor of the client.

## Automated migration

The [`osapilint`](cmd/osapilint/README.md) tool assists with this upgrade but does not fully automate it. v2 -> v3 is the project's one structural boundary - the function-based request API (`opensearchapi.<X>Request{...}.Do(ctx, client)`) became the typed sub-client API described below - so the tool bumps the import path, rewrites the two seed root-client ops (`Ping`, `Indices.Exists`) best-effort, and reports every other call and response-handling change as a `MANUAL` worklist item rather than guess a rewrite it cannot prove. Treat its dry-run output as a migration checklist for the sections below, not a finished rewrite. See [the v2 -> v3 hop](cmd/osapilint/README.md#the-v2---v3-hop) in the tool README for exactly what it does and does not touch.

## Client Creation

You now create the client from the opensearchapi package instead of opensearch. This was done to make the different APIs independent from each other. Plugin APIs like Security get their own folder and therefore their own sub-lib.

Before 3.0.0:

```go
// default client
client, err := opensearch.NewDefaultClient()

// with config
client, err := opensearch.NewClient(
    opensearch.Config{
		Addresses: []string{"https://localhost:9200"},
		Username:  "admin",
		Password:  "admin",
	},
)
```

With 3.0.0:

```go
// default client
client, err := opensearchapi.NewDefaultClient()

// with config
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
		Client: opensearch.Config{
			Addresses:          []string{"https://localhost:9200"},
			Username:           "admin", // For testing only. Don't store credentials in code.
			Password:           "admin",
		},
	},
)
```

## Requests

Prior version 3.0.0 there were two options on how to perform requests. You could either use the request struct of the wished function and execute it with the client .Do() function or use the client function and add wanted args with so called With<arg>() functions. With the new version you now use functions attached to the client and give a context and the wanted request body as argument.

Before 3.0.0:

```go
// using the client function and adding args by using the With<arg>() functions
createIndex, err := client.Indices.Create(
    "some-index",
    client.Indices.Create.WithContext(ctx),
    client.Indices.Create.WithBody(strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`)),
)

// using the request struct
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}
createIndexResponse, err := createIndex.Do(ctx, client)
```

With 3.0.0:

```go
createIndexResponse, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
```

## Responses

With the version 3.0.0 the lib no longer returns the opensearch.Response which is just a wrap up http.Response. Instead it will check the response for errors and try to parse the body into existing structs. Please note that some responses are so complex that we parse them as [json.RawMessage](https://pkg.go.dev/encoding/json#RawMessage) so you can parse them to your expected struct. If you need the opensearch.Response, then you can call .Inspect().

Before 3.0.0:

```go
// Create the request
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}
// Execute the requests
resp, err := createIndex.Do(ctx, client)
if err != nil {
	return err
}
// Close the body
defer resp.Body.Close()

// Check if the status code is >299
if resp.IsError() {
	return fmt.Errorf("Opensearch Returned an error: %#v", resp)
}

// Create a struct that represents the create index response
createResp := struct {
	Acknowledged       bool   `json:"acknowledged"`
	ShardsAcknowledged bool   `json:"shards_acknowledged"`
	Index              string `json:"index"`
}{}

// Try to parse the response into the created struct
if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
	return fmt.Errorf("unexpected response body: %d, %#v, %s", resp.StatusCode, resp.Body, err)
}
// Print the created index name
fmt.Println(createResp.Index)
```

With 3.0.0:

```go
// Create and execute the requests
createResp, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
if err != nil {
	return err
}
// Print the created index name
fmt.Println(createResp.Index)

// To get the opensearch.Response/http.Response
rawResp := createResp.Inspect().Response
```

## Error Handling

With opensearch-go >= 3.0.0 opensearchapi responses are now checked for errors. Checking for errors twice is no longer needed.

Prior versions only returned an error if the request failed to execute. For example if the client can't reach the server or the TLS handshake failed. With opensearch-go >= 3.0.0 each opensearchapi requests will return an error if the response http status code is > 299. The error can be parsed into the new `opensearchapi.Error` type by using `errors.As` to match for exceptions and get a more detailed view.

Before 3.0.0:

```go
// Create the request
createIndex := opensearchapi.IndicesCreateRequest{
    Index: "some-index",
    Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
}

// Execute the requests
resp, err := createIndex.Do(ctx, client)
if err != nil {
	return err
}
// Close the body
defer resp.Body.Close()

// Check if the status code is >299
if resp.IsError() {
    return fmt.Errorf("Opensearch returned an error. Status: %d", resp.StatusCode)
}
```

With 3.0.0:

```go
var opensearchError opensearchapi.Error
// Create and execute the requests
createResp, err := client.Indices.Create(
    ctx,
    opensearchapi.IndicesCreateReq{
        Index: "some-index",
        Body:  strings.NewReader(`{"settings":{"index":{"number_of_shards":4}}}`),
    },
)
// Load err into opensearchapi.Error to access the fields and tolerate if the index already exists
if err != nil {
	if errors.As(err, &opensearchError) {
		if opensearchError.Err.Type != "resource_already_exists_exception" {
			return err
		}
	} else {
		return err
	}
}
```

## API Reorganization

Version 3.0.0 reorganized APIs into logical sub-clients. The following tables cover every method that moved, was renamed, or was removed.

**Naming conventions changed:**

- Request types: `*Request` to `*Req` (e.g., `SearchRequest` to `SearchReq`)
- Query parameters: separate `*Params` sub-struct (e.g., `SearchParams`)
- Functional options (`With*`) removed entirely
- `req.Do(ctx, client)` pattern removed; use `client.Method(ctx, req)` instead

### Document Operations -- Moved to `client.Document`

| Before 3.0.0                          | With 3.0.0                                          |
| ------------------------------------- | --------------------------------------------------- |
| `client.Create(...)`                  | `client.Document.Create(ctx, req)`                  |
| `client.Delete(...)`                  | `client.Document.Delete(ctx, req)`                  |
| `client.DeleteByQuery(...)`           | `client.Document.DeleteByQuery(ctx, req)`           |
| `client.DeleteByQueryRethrottle(...)` | `client.Document.DeleteByQueryRethrottle(ctx, req)` |
| `client.Exists(...)`                  | `client.Document.Exists(ctx, req)`                  |
| `client.ExistsSource(...)`            | `client.Document.ExistsSource(ctx, req)`            |
| `client.Explain(...)`                 | `client.Document.Explain(ctx, req)`                 |
| `client.Get(...)`                     | `client.Document.Get(ctx, req)`                     |
| `client.GetSource(...)`               | `client.Document.Source(ctx, req)`                  |

### Scroll Operations -- Moved to `client.Scroll`

| Before 3.0.0              | With 3.0.0                       |
| ------------------------- | -------------------------------- |
| `client.ClearScroll(...)` | `client.Scroll.Delete(ctx, req)` |
| `client.Scroll(...)`      | `client.Scroll.Get(ctx, req)`    |

### Script Operations -- Moved to `client.Script`

| Before 3.0.0                         | With 3.0.0                                |
| ------------------------------------ | ----------------------------------------- |
| `client.DeleteScript(...)`           | `client.Script.Delete(ctx, req)`          |
| `client.GetScript(...)`              | `client.Script.Get(ctx, req)`             |
| `client.GetScriptContext(...)`       | `client.Script.Context(ctx, req)`         |
| `client.GetScriptLanguages(...)`     | `client.Script.Language(ctx, req)`        |
| `client.PutScript(...)`              | `client.Script.Put(ctx, req)`             |
| `client.ScriptsPainlessExecute(...)` | `client.Script.PainlessExecute(ctx, req)` |

### Index Alias, Mapping, Settings -- Moved to Nested Sub-clients

| Before 3.0.0                          | With 3.0.0                               |
| ------------------------------------- | ---------------------------------------- |
| `client.Indices.DeleteAlias(...)`     | `client.Indices.Alias.Delete(ctx, req)`  |
| `client.Indices.ExistsAlias(...)`     | `client.Indices.Alias.Exists(ctx, req)`  |
| `client.Indices.GetAlias(...)`        | `client.Indices.Alias.Get(ctx, req)`     |
| `client.Indices.PutAlias(...)`        | `client.Indices.Alias.Put(ctx, req)`     |
| `client.Indices.UpdateAliases(...)`   | `client.Aliases(ctx, req)`               |
| `client.Indices.GetMapping(...)`      | `client.Indices.Mapping.Get(ctx, req)`   |
| `client.Indices.PutMapping(...)`      | `client.Indices.Mapping.Put(ctx, req)`   |
| `client.Indices.GetFieldMapping(...)` | `client.Indices.Mapping.Field(ctx, req)` |
| `client.Indices.GetSettings(...)`     | `client.Indices.Settings.Get(ctx, req)`  |
| `client.Indices.PutSettings(...)`     | `client.Indices.Settings.Put(ctx, req)`  |

### Templates -- Moved to Top-level Sub-clients

| Before 3.0.0                                  | With 3.0.0                                     |
| --------------------------------------------- | ---------------------------------------------- |
| `client.Indices.DeleteIndexTemplate(...)`     | `client.IndexTemplate.Delete(ctx, req)`        |
| `client.Indices.ExistsIndexTemplate(...)`     | `client.IndexTemplate.Exists(ctx, req)`        |
| `client.Indices.GetIndexTemplate(...)`        | `client.IndexTemplate.Get(ctx, req)`           |
| `client.Indices.PutIndexTemplate(...)`        | `client.IndexTemplate.Create(ctx, req)`        |
| `client.Indices.SimulateIndexTemplate(...)`   | `client.IndexTemplate.SimulateIndex(ctx, req)` |
| `client.Indices.SimulateTemplate(...)`        | `client.IndexTemplate.Simulate(ctx, req)`      |
| `client.Indices.DeleteTemplate(...)`          | `client.Template.Delete(ctx, req)`             |
| `client.Indices.ExistsTemplate(...)`          | `client.Template.Exists(ctx, req)`             |
| `client.Indices.GetTemplate(...)`             | `client.Template.Get(ctx, req)`                |
| `client.Indices.PutTemplate(...)`             | `client.Template.Create(ctx, req)`             |
| `client.Cluster.DeleteComponentTemplate(...)` | `client.ComponentTemplate.Delete(ctx, req)`    |
| `client.Cluster.ExistsComponentTemplate(...)` | `client.ComponentTemplate.Exists(ctx, req)`    |
| `client.Cluster.GetComponentTemplate(...)`    | `client.ComponentTemplate.Get(ctx, req)`       |
| `client.Cluster.PutComponentTemplate(...)`    | `client.ComponentTemplate.Create(ctx, req)`    |

### Data Streams -- Moved to `client.DataStream`

| Before 3.0.0                             | With 3.0.0                           |
| ---------------------------------------- | ------------------------------------ |
| `client.Indices.CreateDataStream(...)`   | `client.DataStream.Create(ctx, req)` |
| `client.Indices.DeleteDataStream(...)`   | `client.DataStream.Delete(ctx, req)` |
| `client.Indices.GetDataStream(...)`      | `client.DataStream.Get(ctx, req)`    |
| `client.Indices.GetDataStreamStats(...)` | `client.DataStream.Stats(ctx, req)`  |

### Snapshot Repository -- Moved to `client.Snapshot.Repository`

| Before 3.0.0                             | With 3.0.0                                     |
| ---------------------------------------- | ---------------------------------------------- |
| `client.Snapshot.CreateRepository(...)`  | `client.Snapshot.Repository.Create(ctx, req)`  |
| `client.Snapshot.DeleteRepository(...)`  | `client.Snapshot.Repository.Delete(ctx, req)`  |
| `client.Snapshot.GetRepository(...)`     | `client.Snapshot.Repository.Get(ctx, req)`     |
| `client.Snapshot.CleanupRepository(...)` | `client.Snapshot.Repository.Cleanup(ctx, req)` |
| `client.Snapshot.VerifyRepository(...)`  | `client.Snapshot.Repository.Verify(ctx, req)`  |

### Ingest -- Renamed Methods

| Before 3.0.0                        | With 3.0.0                       |
| ----------------------------------- | -------------------------------- |
| `client.Ingest.PutPipeline(...)`    | `client.Ingest.Create(ctx, req)` |
| `client.Ingest.DeletePipeline(...)` | `client.Ingest.Delete(ctx, req)` |
| `client.Ingest.GetPipeline(...)`    | `client.Ingest.Get(ctx, req)`    |
| `client.Ingest.ProcessorGrok(...)`  | `client.Ingest.Grok(ctx, req)`   |

### Dangling Indices -- Moved to `client.Dangling`

| Before 3.0.0                                     | With 3.0.0                         |
| ------------------------------------------------ | ---------------------------------- |
| `client.DanglingIndicesDeleteDanglingIndex(...)` | `client.Dangling.Delete(ctx, req)` |
| `client.DanglingIndicesImportDanglingIndex(...)` | `client.Dangling.Import(ctx, req)` |
| `client.DanglingIndicesListDanglingIndices(...)` | `client.Dangling.Get(ctx, req)`    |

### Other Renames

| Before 3.0.0                             | With 3.0.0                              |
| ---------------------------------------- | --------------------------------------- |
| `client.Count(...)`                      | `client.Indices.Count(ctx, req)`        |
| `client.FieldCaps(...)`                  | `client.Indices.FieldCaps(ctx, req)`    |
| `client.Mget(...)`                       | `client.MGet(ctx, req)`                 |
| `client.Msearch(...)`                    | `client.MSearch(ctx, req)`              |
| `client.MsearchTemplate(...)`            | `client.MSearchTemplate(ctx, req)`      |
| `client.Mtermvectors(...)`               | `client.MTermvectors(ctx, req)`         |
| `client.Indices.AddBlock(...)`           | `client.Indices.Block(ctx, req)`        |
| `client.Indices.ResolveIndex(...)`       | `client.Indices.Resolve(ctx, req)`      |
| `client.Cat.Fielddata(...)`              | `client.Cat.FieldData(ctx, req)`        |
| `client.Cat.Nodeattrs(...)`              | `client.Cat.NodeAttrs(ctx, req)`        |
| `client.Nodes.ReloadSecureSettings(...)` | `client.Nodes.ReloadSecurity(ctx, req)` |

### Removed APIs (no v3+ equivalent)

- `client.TermsEnum(...)`
- `client.Cat.Help(...)`
- `client.Indices.DiskUsage(...)`
- `client.Indices.FieldUsageStats(...)`
- `client.Indices.GetUpgrade(...)`
- `client.Indices.Upgrade(...)`
