- [Upgrading Opensearch GO Client](#upgrading-opensearch-go-client)
    - [Upgraading to >= 3.0.0](#upgrading-to->=-3.0.0)
        - [opensearchapi](#opensearchapi-snapshot-delete)
        - [opensearchapi](#opensearchapi-error-handling)

# Upgrading Opensearch GO Client

## Upgrading to >= 3.0.0

### opensearchapi snapshot delete

`SnapshotDeleteRequest` and `SnapshotDelete` changed the argument `Snapshot` type from `string` to `[]string`.

Before 3.0.0:

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

With 3.0.0:

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
```

### opensearchapi error handling

With opensearch-go >= 3.0.0 opensearchapi responses are now checked for errors. Checking for errors twice is no longer needed.

Prior versions only returned an error if the request failed to execute. For example if the client can't reach the server or the TLS handshake failed. 
With opensearch-go >= 3.0.0 each opensearchapi requests will return an error if the response http status code is > 299.
The error can be parsed into the new `opensearchapi.Error` type by using `errors.As` to match for exceptions and get a more detailed view. 

Before 3.0.0:
```go
createIndex := opensearchapi.IndicesCreateRequest{
  Index: IndexName,
  Body:  mapping,
}

ctx := context.Background()
createIndexResp, err := createIndex.Do(ctx, client)
if err != nil {
    return err
}
if createIndexResp.IsError() {
    fmt.Errorf("Opensearch returned an error. Status: %d", createIndexResp.StatusCode)
}
```

With 3.0.0:

```go
createIndex := opensearchapi.IndicesCreateRequest{
  Index: IndexName,
  Body:  mapping,
}

ctx := context.Background()
var opensearchError *opensearchapi.Error

createIndexResponse, err := createIndex.Do(ctx, client)
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

