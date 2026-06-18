# Upgrading to >= 2.3.0

## Snapshot Delete

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
