# osapi/plugins

Each plugin is an independent package that wraps a shared `opensearch.Client` to provide strongly-typed access to an OpenSearch plugin's REST API. Plugins are generated from the same OpenAPI specification as the core `osapi` package.

## Usage Pattern

All plugins follow the same constructor pattern:

```go
import (
    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/osapi/plugins/knn"
)

// Create the shared transport client
root, err := opensearch.NewClient(opensearch.Config{
    Addresses: []string{"https://localhost:9200"},
    Username:  "admin",
    Password:  "admin",
})
if err != nil {
    log.Fatal(err)
}

// Wrap it in a plugin client
knnClient := knn.NewClient(root)

// Call plugin operations
resp, err := knnClient.Stats(ctx, nil)
```

## Combining with the Core Client

The plugin client and core `osapi.Client` can share the same underlying transport:

```go
import (
    "github.com/opensearch-project/opensearch-go/v4"
    "github.com/opensearch-project/opensearch-go/v4/osapi"
    "github.com/opensearch-project/opensearch-go/v4/osapi/plugins/knn"
    "github.com/opensearch-project/opensearch-go/v4/osapi/plugins/security"
)

root, _ := opensearch.NewClient(opensearch.Config{...})

// Core API client
api := osapi.NewFromClient(root)

// Plugin clients (same connection pool, auth, retry logic)
knnClient := knn.NewClient(root)
secClient := security.NewClient(root)

// Use both
_, _ = api.Indices.Create(ctx, osapi.IndicesCreateReq{Index: "vectors"})
_, _ = knnClient.Stats(ctx, nil)
_, _ = secClient.GetUser(ctx, nil)
```

## Available Plugins

| Package               | Import Path                       | Operations | Description                             |
|-----------------------|-----------------------------------|:----------:|-----------------------------------------|
| `asynchronous_search` | `.../plugins/asynchronous_search` | 4          | Submit and manage async search requests |
| `flow_framework`      | `.../plugins/flow_framework`      | 10         | Workflow automation and templates       |
| `geospatial`          | `.../plugins/geospatial`          | 7          | Geospatial data and queries             |
| `ingestion`           | `.../plugins/ingestion`           | 3          | Data ingestion management               |
| `insights`            | `.../plugins/insights`            | 1          | Query and cluster insights              |
| `ism`                 | `.../plugins/ism`                 | 12         | Index State Management policies         |
| `knn`                 | `.../plugins/knn`                 | 6          | k-Nearest Neighbors vector search       |
| `list`                | `.../plugins/list`                | 3          | List-based API queries                  |
| `ltr`                 | `.../plugins/ltr`                 | 26         | Learning to Rank                        |
| `ml`                  | `.../plugins/ml`                  | 74         | Machine Learning models and inference   |
| `neural`              | `.../plugins/neural`              | 1          | Neural search operations                |
| `notifications`       | `.../plugins/notifications`       | 9          | Notification channels and events        |
| `observability`       | `.../plugins/observability`       | 7          | Observability objects and metrics       |
| `ppl`                 | `.../plugins/ppl`                 | 4          | Piped Processing Language queries       |
| `query`               | `.../plugins/query`               | 5          | Query workbench operations              |
| `replication`         | `.../plugins/replication`         | 11         | Cross-cluster replication               |
| `rollups`             | `.../plugins/rollups`             | 6          | Index rollup jobs                       |
| `search_relevance`    | `.../plugins/search_relevance`    | 18         | Search relevance tooling                |
| `security`            | `.../plugins/security`            | 76         | Security (roles, users, tenants, auth)  |
| `security_analytics`  | `.../plugins/security_analytics`  | 3          | Security analytics and detectors        |
| `sm`                  | `.../plugins/sm`                  | 8          | Snapshot Management policies            |
| `sql`                 | `.../plugins/sql`                 | 6          | SQL and PPL query interface             |
| `transforms`          | `.../plugins/transforms`          | 8          | Index transforms                        |
| `ubi`                 | `.../plugins/ubi`                 | 1          | User Behavior Insights                  |
| `wlm`                 | `.../plugins/wlm`                 | 4          | Workload Management                     |

All import paths are prefixed with `github.com/opensearch-project/opensearch-go/v4/osapi/plugins/`.

## Request/Response Pattern

Plugins use the same Req/Resp/Params triple as the core package:

```go
resp, err := knnClient.SearchModels(ctx, &knn.SearchModelsReq{
    Body: strings.NewReader(`{"query":{"match_all":{}}}`),
})
if err != nil {
    log.Fatal(err)
}

// Typed fields
fmt.Println(resp.Hits.Total.Value)

// Raw response access
raw := resp.Inspect().Response
fmt.Println(raw.StatusCode)
```

Operations with all-optional fields accept a nil pointer for defaults:

```go
resp, err := knnClient.Stats(ctx, nil)
```
