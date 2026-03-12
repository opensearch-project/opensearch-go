- [Upgrading OpenSearch Go Client](#upgrading-opensearch-go-client)
  - [Upgrading to >= 5.0.0](#upgrading-to->=-5.0.0)
    - [StringError for unknown JSON responses](#stringerror-for-unknown-json-responses)
  - [Upgrading to >= 4.0.0](#upgrading-to->=-4.0.0)
    - [Import path](#import-path)
    - [Error types](#error-types)
    - [AWS signer](#aws-signer)
  - [Upgrading to >= 3.0.0](#upgrading-to->=-3.0.0)
    - [Client creation](#client-creation)
    - [Requests](#requests)
    - [Responses](#responses)
    - [Error handling](#error-handling)
    - [API reorganization](#api-reorganization)
  - [Upgrading to >= 2.3.0](#upgrading-to->=-2.3.0)
    - [Snapshot delete](#snapshot-delete)

# Upgrading OpenSearch Go Client

## Upgrading to >= 5.0.0

### StringError for Unknown JSON Responses

Version 5.0.0 returns `*opensearch.StringError` error type instead of `*fmt.wrapError` when response received from the server is an unknown JSON. For example, consider delete document API which returns an unknown JSON body when document is not found.

Before 5.0.0:

```go
docDelResp, err = client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: "movies", DocumentID: "3"})
if err != nil {
	fmt.Println(err)

	if !errors.Is(err, opensearch.ErrJSONUnmarshalBody) && docDelResp != nil {
		resp := docDelResp.Inspect().Response
		// get http status
		fmt.Println(resp.StatusCode)
		body := strings.TrimPrefix(err.Error(), "opensearch error response could not be parsed as error: ")
		errResp := opensearchapi.DocumentDeleteResp{}
		json.Unmarshal([]byte(body), &errResp)
		// extract result field from the body
		fmt.Println(errResp.Result)
	}
}
```

After 5.0.0:

```go
docDelResp, err = client.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: "movies", DocumentID: "3"})
if err != nil {
	// parse into *opensearch.StringError
	var myStringErr *opensearch.StringError
	if errors.As(err, &myStringErr) {
		// get http status
		fmt.Println(myStringErr.Status)
		errResp := opensearchapi.DocumentDeleteResp{}
		json.Unmarshal([]byte(myStringErr.Err), &errResp)
		// extract result field from the body
		fmt.Println(errResp.Result)
	}
}
```

## Upgrading to >= 4.0.0

Version 4.0.0 updated the module import path, moved error types from opensearchapi to opensearch, renamed them, added new error types, and migrated the `signer/aws` package from AWS SDK v1 to AWS SDK v2.

### Import Path

Update all import paths from `v3` to `v4`:

```go
// Before (v3)
import (
    "github.com/opensearch-project/opensearch-go/v3"
    "github.com/opensearch-project/opensearch-go/v3/opensearchapi"
)

// After (v4)
import (
    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)
```

Update your `go.mod`:

```bash
go get github.com/opensearch-project/opensearch-go/v4@latest
```

### Error Types

Before 4.0.0: Error types:

- `opensearchapi.Error`
- `opensearchapi.StringError`

With 4.0.0: Error types

- `opensearch.Error` -- base error with string `Err` field
- `opensearch.StringError` -- raw string error body
- `opensearch.ReasonError` -- error with `Reason` and `Status` fields
- `opensearch.MessageError` -- error with `Message` field
- `opensearch.StructError` -- structured JSON error with `Type`, `Reason`, `RootCause` (was `opensearchapi.Error`)

Update `errors.As` targets to use `opensearch.*` instead of `opensearchapi.*`:

```go
// Before (v3)
var opensearchError *opensearchapi.Error
if errors.As(err, &opensearchError) {
    fmt.Println(opensearchError.Err.Type)
}

// After (v4)
var opensearchError *opensearch.StructError
if errors.As(err, &opensearchError) {
    fmt.Println(opensearchError.Err.Type)
}
```

### AWS Signer

The `signer/aws` package now uses AWS SDK v2 instead of AWS SDK v1. AWS SDK v1 reached end-of-support on July 31, 2025.

Before 4.0.0 (AWS SDK v1):

```go
import (
    "github.com/aws/aws-sdk-go/aws/session"
    signer "github.com/opensearch-project/opensearch-go/v3/signer/aws"
)

awsSigner, err := signer.NewSigner(session.Options{
    Config: aws.Config{Region: aws.String("us-east-1")},
})
```

With 4.0.0 (AWS SDK v2):

```go
import (
    "context"
    "github.com/aws/aws-sdk-go-v2/config"
    signer "github.com/opensearch-project/opensearch-go/v4/signer/aws"
)

cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion("us-east-1"))
if err != nil {
    return err
}
awsSigner, err := signer.NewSigner(cfg)
```

The `signer/awsv2` package (which already used AWS SDK v2) remains available at `github.com/opensearch-project/opensearch-go/v4/signer/awsv2` with the same API.

## Upgrading to >= 3.0.0

Version 3.0.0 is a major refactor of the client.

### Client Creation

You now create the client from the opensearchapi package instead of opensearch. This was done to make the different APIs independent from each other. Plugin APIs like Security get their own folder and therefore their own sub-lib.

Before 3.0.0:

```go
// default client
client, err := opensearch.NewDefaultClient()

// with config
client, err := opensearch.NewClient(
    opensearch.Config{
	    InsecureSkipVerify: true,
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
			InsecureSkipVerify: true, // For testing only. Use certificate for validation.
			Addresses:          []string{"https://localhost:9200"},
			Username:           "admin", // For testing only. Don't store credentials in code.
			Password:           "admin",
		},
	},
)
```

### Requests

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

### Responses

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

### Error Handling

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
if createIndexResp.IsError() {
    fmt.Errorf("Opensearch returned an error. Status: %d", createIndexResp.StatusCode)
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

### API Reorganization

Version 3.0.0 reorganized APIs into logical sub-clients. The following tables cover every method that moved, was renamed, or was removed.

**Naming conventions changed:**

- Request types: `*Request` to `*Req` (e.g., `SearchRequest` to `SearchReq`)
- Query parameters: separate `*Params` sub-struct (e.g., `SearchParams`)
- Functional options (`With*`) removed entirely
- `req.Do(ctx, client)` pattern removed; use `client.Method(ctx, req)` instead

#### Document Operations -- Moved to `client.Document`

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

#### Scroll Operations -- Moved to `client.Scroll`

| Before 3.0.0              | With 3.0.0                       |
| ------------------------- | -------------------------------- |
| `client.ClearScroll(...)` | `client.Scroll.Delete(ctx, req)` |
| `client.Scroll(...)`      | `client.Scroll.Get(ctx, req)`    |

#### Script Operations -- Moved to `client.Script`

| Before 3.0.0                         | With 3.0.0                                |
| ------------------------------------ | ----------------------------------------- |
| `client.DeleteScript(...)`           | `client.Script.Delete(ctx, req)`          |
| `client.GetScript(...)`              | `client.Script.Get(ctx, req)`             |
| `client.GetScriptContext(...)`       | `client.Script.Context(ctx, req)`         |
| `client.GetScriptLanguages(...)`     | `client.Script.Language(ctx, req)`        |
| `client.PutScript(...)`              | `client.Script.Put(ctx, req)`             |
| `client.ScriptsPainlessExecute(...)` | `client.Script.PainlessExecute(ctx, req)` |

#### Index Alias, Mapping, Settings -- Moved to Nested Sub-clients

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

#### Templates -- Moved to Top-level Sub-clients

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

#### Data Streams -- Moved to `client.DataStream`

| Before 3.0.0                             | With 3.0.0                           |
| ---------------------------------------- | ------------------------------------ |
| `client.Indices.CreateDataStream(...)`   | `client.DataStream.Create(ctx, req)` |
| `client.Indices.DeleteDataStream(...)`   | `client.DataStream.Delete(ctx, req)` |
| `client.Indices.GetDataStream(...)`      | `client.DataStream.Get(ctx, req)`    |
| `client.Indices.GetDataStreamStats(...)` | `client.DataStream.Stats(ctx, req)`  |

#### Snapshot Repository -- Moved to `client.Snapshot.Repository`

| Before 3.0.0                             | With 3.0.0                                     |
| ---------------------------------------- | ---------------------------------------------- |
| `client.Snapshot.CreateRepository(...)`  | `client.Snapshot.Repository.Create(ctx, req)`  |
| `client.Snapshot.DeleteRepository(...)`  | `client.Snapshot.Repository.Delete(ctx, req)`  |
| `client.Snapshot.GetRepository(...)`     | `client.Snapshot.Repository.Get(ctx, req)`     |
| `client.Snapshot.CleanupRepository(...)` | `client.Snapshot.Repository.Cleanup(ctx, req)` |
| `client.Snapshot.VerifyRepository(...)`  | `client.Snapshot.Repository.Verify(ctx, req)`  |

#### Ingest -- Renamed Methods

| Before 3.0.0                        | With 3.0.0                       |
| ----------------------------------- | -------------------------------- |
| `client.Ingest.PutPipeline(...)`    | `client.Ingest.Create(ctx, req)` |
| `client.Ingest.DeletePipeline(...)` | `client.Ingest.Delete(ctx, req)` |
| `client.Ingest.GetPipeline(...)`    | `client.Ingest.Get(ctx, req)`    |
| `client.Ingest.ProcessorGrok(...)`  | `client.Ingest.Grok(ctx, req)`   |

#### Dangling Indices -- Moved to `client.Dangling`

| Before 3.0.0                                     | With 3.0.0                         |
| ------------------------------------------------ | ---------------------------------- |
| `client.DanglingIndicesDeleteDanglingIndex(...)` | `client.Dangling.Delete(ctx, req)` |
| `client.DanglingIndicesImportDanglingIndex(...)` | `client.Dangling.Import(ctx, req)` |
| `client.DanglingIndicesListDanglingIndices(...)` | `client.Dangling.Get(ctx, req)`    |

#### Other Renames

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

#### Removed APIs (no v3+ equivalent)

- `client.TermsEnum(...)`
- `client.Cat.Help(...)`
- `client.Indices.DiskUsage(...)`
- `client.Indices.FieldUsageStats(...)`
- `client.Indices.GetUpgrade(...)`
- `client.Indices.Upgrade(...)`

## Upgrading to >= 2.3.0

### Snapshot Delete

`SnapshotDeleteRequest` and `SnapshotDelete` changed the argument `Snapshot` type from `string` to `[]string`.

Before 2.3.0:

```go
// If you have a string containing your snapshot
stringSnapshotsToDelete := "snapshot-1,snapshot-2"
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
	Snapshot: stringSnapshotsToDelete,
}

// If you have a slice of strings containing your snapshot
sliceSnapshotToDelete := []string{"snapshot-1","snapshot-2"}
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot: strings.Join(sliceSnapshotsToDelete, ","),
}
```

With 2.3.0:

```go
// If you have a string containing your snapshots
stringSnapshotsToDelete := strings.Split("snapshot-1,snapshot-2", ",")
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot:   stringSnapshotsToDelete,
}

// If you have a slice of strings containing your snapshots
sliceSnapshotToDelete := []string{"snapshot-1", "snapshot-2"}
reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
  Repository: repo,
  Snapshot: sliceSnapshotsToDelete,
}
```
