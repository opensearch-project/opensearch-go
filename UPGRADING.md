- [Upgrading Opensearch GO Client](#upgrading-opensearch-go-client)
    - [Upgraading to >= 3.0.0](#upgrading-to->=-3.0.0)
        - [opensearchapi](#opensearchapi-snapshot-delete)

# Upgrading Opensearch GO Client

## Upgrading to >= 3.0.0

### opensearchapi snapshot delete
SnapshotDeleteRequest and SnapshotDelete changed the argument `Snapshot` type from `string` to `[]string`.
Before:
```go
    // If you have a string containing your snapshot
    stringSnapshotsToDelete := "snapshot-1,snapshot-2"
	reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
		Repository: repo,
		Snapshot:   stringSnapshotsToDelete,
	}

    // If you have a slice of strings containing your snapshot
    sliceSnapshotToDelete := []string{"snapshot-1","snapshot-2"}
	reqSnapshots := &opensearchapi.SnapshotDeleteRequest{
		Repository: repo,
		Snapshot:   strings.Join(sliceSnapshotsToDelete, ","),
	}
```

After:
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
		Snapshot:   sliceSnapshotsToDelete,
	}
```
