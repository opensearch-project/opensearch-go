# Configure the client with retry and backoff

The OpenSearch client will retry on certain errors, such as `503 Service Unavailable`. And it will retry right after receiving the error. You can customize the retry behavior.

## Setup

Let's create a client instance:

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {
	client, err := opensearchapi.NewClient(opensearchapi.Config{
        // Retry on 429 TooManyRequests statuses as well (502, 503, 504 are default values)
        //
        RetryOnStatus: []int{502, 503, 504, 429},

        // A simple incremental backoff function
        //
        RetryBackoff: func(i int) time.Duration { return time.Duration(i) * 100 * time.Millisecond },

        // Retry up to 5 attempts (1 initial + 4 retries)
        //
        MaxRetries: 4,
	})
	if err != nil {
		return err
	}
```

If you do not want to wait too long when the server is not responsive, then control the total duration of the requests with a context. The on-going request and the backoff will be canceled when the context is canceled.

```go
	rootCtx := context.Background()
	ctx := context.WithTimeout(rootCtx, time.Second)

	infoResp, err := client.Info(ctx, nil)
	return nil
}
```
