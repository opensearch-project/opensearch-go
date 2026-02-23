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
                RetryOnStatus: []int{502, 503, 504, 429},

                // A simple incremental backoff function
                RetryBackoff: func(i int) time.Duration { return time.Duration(i) * 100 * time.Millisecond },

                // Retry up to 5 attempts (1 initial + 4 retries)
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

## Dead Connection Resurrection

For details on the health check endpoint itself -- response fields, HTTP status codes, required permissions, and security configuration -- see [cluster_health_checking.md](cluster_health_checking.md).

When a node becomes unreachable, the client marks it as dead and schedules periodic resurrection attempts using exponential backoff. The retry interval is adapted to cluster health so the client is aggressive when all nodes are down (to recover capacity fast) and conservative when the cluster is mostly healthy (to avoid TLS handshake storms on recovering servers).

### How It Works

Each dead connection gets its own resurrection goroutine. The retry interval is determined by three competing inputs -- the largest one wins:

1. **Health-ratio timeout**: Exponential backoff scaled by cluster health. `baseTimeout * (liveNodes / totalNodes)`. Healthy clusters wait longer.

2. **Rate-limited timeout**: Throttles based on estimated TLS handshake pressure. `(liveNodes * clientsPerServer) / serverMaxNewConnsPerSec`. Grows as servers recover, because recovering servers face reconnections from ALL clients. The capacity model values are auto-derived from the server's core count (discovered via `/_nodes/http,os`, default: 8 cores).

3. **Minimum floor**: `MinimumResurrectTimeout` (default: 500ms). Absolute lower bound.

```
Final timeout = max(healthTimeout, rateLimitedTimeout, minimumFloor) + jitter
```

### Default Behavior

With default settings, a 150-node cluster recovering from a full outage:

```
Live  Dead  Retry Interval  Why
----  ----  --------------  ---
  0    150  500ms           All dead -> minimum floor, need capacity back
 10    140  2.5s            Rate limit: (10 * 8) / 32 = 2.5s
 50    100  12.5s           Rate limit: (50 * 8) / 32 = 12.5s
100     50  30s             Rate limit capped at max (30s)
149      1  30s             Nearly healthy, very conservative
```

### Detailed Math: 150-Node Cluster

The table below shows every input to the timeout formula as a 150-node cluster recovers from a full outage. Base timeout is capped at 30s (failures past cutoff). Jitter omitted for clarity.

```
Live   Dead   Health Timeout     Rate Limit              Final Timeout
Nodes  Nodes  base*(live/total)  (live*8)/32             max(health, rate, 500ms)
-----  -----  -----------------  ----------------------  -------------------------
  0     150   30s * 0.00 =  0s    (0  *  8)/32 =    0s   500ms <- all dead: aggressive
  5     145   30s * 0.03 =  1s    (5  *  8)/32 =  1.3s   1.3s
 10     140   30s * 0.07 =  2s    (10 *  8)/32 =  2.5s   2.5s
 25     125   30s * 0.17 =  5s    (25 *  8)/32 =  6.3s   6.3s
 50     100   30s * 0.33 = 10s    (50 *  8)/32 = 12.5s   12.5s <- rate limit dominates
 75      75   30s * 0.50 = 15s    (75 *  8)/32 = 18.8s   18.8s
100      50   30s * 0.67 = 20s   (100 *  8)/32 =   25s   25s
149       1   30s * 0.99 = 30s   (149 *  8)/32 = 37.3s   30s  <- capped at max
```

Recovery timeline (150-node cluster, all nodes fail then recover together):

```
Timeout
(seconds)
30 |                                              .............
   |                                         ....
   |                                     ...
   |                                  ..
20 |                               ..
   |                            ..
   |                          .
   |                        .
15 |                      .  <- rate limit: (live * 8) / 32
   |                    .
   |                  .
10 |                .
   |              .
   |            .
   |          .
 5 |        .
   |      .
   |    .
   |  .
 1 | .
0.5|x  <- minimum floor (all dead, most aggressive)
   +--+--+--+--+--+--+--+--+--+--+--+--+--+--+--+-
   0  10 20 30 40 50 60 70 80 90 100   120   140 150
                    Live Nodes ->
```

**Key insight**: the client is MOST aggressive when all servers are down (500ms retries to get capacity back fast) and MOST conservative when the cluster is nearly healthy (30s retries to avoid TLS handshake storms on the few remaining recovering servers).

### Detailed Math: 3-Node Cluster

Small clusters behave differently -- the health-ratio timeout dominates over the rate limit because few nodes produce a tiny rate-limit term:

```
Live  Dead  Health Timeout     Rate Limit           Final Timeout
----  ----  -----------------  -------------------  ------------------------
 0     3    30s * 0.00 =  0s   (0 * 8)/32 =    0s   500ms <- aggressive
 1     2    30s * 0.33 = 10s   (1 * 8)/32 = 0.25s   10s   <- health ratio dominates
 2     1    30s * 0.67 = 20s   (2 * 8)/32 =  0.5s   20s   <- health ratio dominates
```

In small clusters the rate limit is negligible. The health-ratio timeout alone provides proportional backoff: 1 out of 3 nodes dead -> wait 10s, 2 out of 3 -> wait 20s.

### Exponential Backoff Progression

Before reaching the timeout cap, consecutive failures increase the base timeout exponentially. Example: 2 live, 1 dead (healthRatio = 0.67):

```
Failures  Base Timeout           Health Timeout        Final
--------  ---------------------  --------------------  -------
1         5s  * 2^0 =  5s        5s * 0.67 =  3.3s    3.3s
2         5s  * 2^1 = 10s       10s * 0.67 =  6.7s    6.7s
3         5s  * 2^2 = 20s       20s * 0.67 = 13.3s   13.3s
4         5s  * 2^3 = 30s (cap) 30s * 0.67 = 20.0s   20.0s
5+        5s  * 2^4 = 30s (cap) 30s * 0.67 = 20.0s   20.0s  <- steady state
```

After 4 consecutive failures, the timeout reaches steady state at 20s (plus jitter). If the node recovers, its failure counter resets and the next failure starts back at 3.3s.

### Why Rate Limit Uses Live Nodes (Not Dead Nodes)

The rate limit formula uses `liveNodes`, not `deadNodes`, because the bottleneck is the **recovering** servers -- not the dead ones:

- **Dead servers** can't do anything; they're unreachable. Retrying them faster doesn't create load anywhere.
- **Recovering servers** (transitioning from dead to live) are the bottleneck. Each one must handle TLS handshake negotiations from every client simultaneously. Async cryptographic operations are expensive compared to established TLS connections.
- As more servers come back, the aggregate TLS handshake load **increases** because all clients reconnect to all recovering servers at once. The rate limit grows with `liveNodes` to account for this increasing pressure.

### Configuration

```go
client, err := opensearchapi.NewClient(opensearchapi.Config{
    // Exponential backoff for dead connections
    ResurrectTimeoutInitial: 5 * time.Second,        // Starting backoff (default: 5s)
    ResurrectTimeoutMax:     30 * time.Second,        // Cap before jitter (default: 30s)
    MinimumResurrectTimeout: 500 * time.Millisecond,  // Absolute floor (default: 500ms)
    JitterScale:             0.5,                     // Jitter multiplier (default: 0.5)
})
```

The rate-limiting parameters (`clientsPerServer`, `serverMaxNewConnsPerSec`) are auto-derived from the server's core count discovered via `/_nodes/http,os`. With the default of 8 cores: `clientsPerServer=8`, `serverMaxNewConnsPerSec=32`. See [connection_pool.md](connection_pool.md#capacity-model) for details.
