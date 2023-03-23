- [Upgrading Opensearch GO Client](#upgrading-opensearch-go-client)
  - [Upgraading to >= 3.0.0](#upgrading-to->=-3.0.0)
    - [opensearchapi](#opensearchapi-error-handling)

# Upgrading Opensearch GO Client

## Upgrading to >= 3.0.0

### opensearchapi error handling

With opensearch-go >= 3.0.0 opensearchapi responses are now checked for errors.
Prior versions only returned an error if the request failed to execute. For
example if the client can't reach the server or the TLS handshake failed. With
opensearch-go >= 3.0.0 each opensearchapi requests will return an error if the
response http status code is > 299. The error can be parsed into the new
opensearchapi.Error type by using `errors.As` to match for exceptions and get a
more detailed view. See the example below.

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
