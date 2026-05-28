- [Security](#security)
  - [TLS and Certificate Verification](#tls-and-certificate-verification)
  - [Credential Management](#credential-management)
  - [Index Names and User-Supplied Input](#index-names-and-user-supplied-input)
    - [How the Client Encodes Path Parameters](#how-the-client-encodes-path-parameters)
    - [Treat External Values as Literals by Default](#treat-external-values-as-literals-by-default)
    - [A Reusable Validation Helper](#a-reusable-validation-helper)
    - [Be Explicit When You Want a Pattern](#be-explicit-when-you-want-a-pattern)
    - [Document IDs and Other Path Parameters](#document-ids-and-other-path-parameters)
    - [Restricting Wildcards (Planned)](#restricting-wildcards-planned)
  - [Request Body Construction](#request-body-construction)
  - [Error Handling and Information Disclosure](#error-handling-and-information-disclosure)
  - [Network and Transport Configuration](#network-and-transport-configuration)
    - [Connection Timeouts](#connection-timeouts)
    - [Custom Transports](#custom-transports)
    - [Node Discovery](#node-discovery)
  - [Quick Reference](#quick-reference)

# Security

This guide covers security best practices for using the OpenSearch Go client. Examples use the `v5preview/opensearchapi` package (spec-generated, typed request bodies, pointer params). The same principles apply to the `opensearchapi` package, which uses `io.Reader` bodies instead of typed structs.

## TLS and Certificate Verification

**IMPORTANT:** Always use TLS with certificate verification in production.

The `InsecureSkipVerify` option exists for local development and testing against self-signed certificates. Using it in production disables certificate verification, which means the client cannot distinguish a legitimate OpenSearch cluster from a man-in-the-middle interceptor. An attacker in this position can read every query and response, including document contents and credentials.

```go
// WRONG: disables certificate verification. Suitable for local development only.
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
        Client: opensearch.Config{
            Addresses:          []string{"https://opensearch.internal:9200"},
            InsecureSkipVerify: true, // Security risk in production
        },
    },
)
```

```go
// CORRECT: provide a CA certificate for verification.
caCert, err := os.ReadFile("/path/to/root-ca.pem")
if err != nil {
    log.Fatalf("reading CA certificate: %s", err)
}

client, err := opensearchapi.NewClient(
    opensearchapi.Config{
        Client: opensearch.Config{
            Addresses: []string{"https://opensearch.internal:9200"},
            CACert:    caCert,
        },
    },
)
```

If you need mutual TLS (client certificate authentication), clone the default transport and configure it as described in the [Custom Transport](../USER_GUIDE.md#custom-transport) section of the User Guide.

## Credential Management

Never embed credentials directly in source code. Credentials in source are easily leaked through version control history, CI logs, error messages, and core dumps.

```go
// WRONG: credentials in source code. Security risk.
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
        Client: opensearch.Config{
            Addresses: []string{"https://opensearch.internal:9200"},
            Username:  "admin",
            Password:  "myStrongPassword123!",
        },
    },
)
```

```go
// CORRECT: credentials from environment or secrets manager.
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
        Client: opensearch.Config{
            Addresses: []string{os.Getenv("OPENSEARCH_URL")},
            Username:  os.Getenv("OPENSEARCH_USERNAME"),
            Password:  os.Getenv("OPENSEARCH_PASSWORD"),
            CACert:    caCert,
        },
    },
)
```

For AWS deployments, use IAM-based authentication with the request signer instead of static credentials. See [Amazon OpenSearch Service](../USER_GUIDE.md#amazon-opensearch-service) in the User Guide.

## Index Names and User-Supplied Input

Many OpenSearch APIs accept an index name, and most of those also accept an index _pattern_: a comma-separated list of names, wildcard expressions such as `logs-*`, date-math expressions such as `<logs-{now/d}>`, and the special tokens `_all` and `*` meaning "every index". OpenSearch calls this multi-target syntax.

This section covers how the Go client passes those values to the server, the security implications, and the recommended patterns for applications that build requests from values they did not author themselves (configuration, RPC parameters, multi-tenant identifiers, and similar).

### How the Client Encodes Path Parameters

Every request struct in `v5preview/opensearchapi` and `opensearchapi` builds its URL through the shared `internal/path` package. For each caller-supplied path segment (an index name, a document ID, a template name, and so on) the client:

- percent-encodes characters that would change the _structure_ of the URL (`/`, `?`, `#`, `%`, whitespace, control bytes, and the `..` sequence), so a value can never escape its segment, add query parameters, or reach a different REST endpoint; and
- passes everything else through unchanged, including `*`, `,`, `-`, `+`, `<`, `>`, and `_`.

The second group is intentional: those characters are how multi-target syntax is expressed on the wire, and the client does not know whether a given string is meant as a literal name or as a pattern. `IndicesDeleteReq{Index: []string{"logs-2024.01.*"}}` and `IndicesDeleteReq{Index: []string{tenantID}}` look identical to the client; only the application knows whether the value on the right is a pattern by design or a literal that happens to contain `*`.

In other words, the client guarantees **URL-structural safety** (your value stays inside one path segment) but does not, and cannot, guarantee **target-selection safety** (your value resolves to exactly the indices you had in mind). That second property depends on what the value _means_, which is application knowledge.

Here are some examples of how structural encoding prevents common injection attacks:

| Input               | Encoded As              | Risk Prevented            |
| ------------------- | ----------------------- | ------------------------- |
| `../../*`           | `..%2F..%2F%2A`         | Path traversal            |
| `idx?pipeline=evil` | `idx%3Fpipeline%3Devil` | Query parameter injection |
| `idx#fragment`      | `idx%23fragment`        | Fragment injection        |
| `100%done`          | `100%25done`            | Double-encoding confusion |
| `logs/2024`         | `logs%2F2024`           | Segment injection         |

This encoding is applied automatically. You do not need to pre-encode values yourself.

### Treat External Values as Literals by Default

When an index name, alias, document ID, or similar identifier originates outside your code (a tenant record, an HTTP request parameter, a job payload), treat it as a **literal name** unless your design explicitly calls for pattern matching at that call site. Concretely:

1. **Validate against your own naming rules first.** Most applications already constrain the identifiers they generate (a tenant slug, a dataset name). Apply that same constraint on the way back in, before the value reaches a request struct. A value that your system could never have produced should be rejected at the boundary, not forwarded to the cluster.

2. **Reserve pattern characters for code, not data.** `*`, `,`, `<`, `>`, `_all` and a leading `-` (exclusion) are the multi-target operators. If a call site is meant to address one specific index, those characters have no business appearing in the value, and their presence is a strong signal that the input did not come from where you expected. The most robust posture is an allow-list of the characters your identifiers actually use, rather than a deny-list of the operators OpenSearch happens to interpret today.

3. **Prefer the slice form over pre-joined strings.** Request fields such as `IndicesDeleteReq.Index` are `[]string`. Pass one element per intended target and let the client join and encode them. Building a single comma-joined string yourself bypasses the per-element encoding and makes it harder to reason about what each element contains.

4. **For destructive operations, scope server-side as well.** OpenSearch provides `action.destructive_requires_name` (cluster setting) and the per-request `expand_wildcards` / `allow_no_indices` parameters. Setting `action.destructive_requires_name: true` causes the server to reject `DELETE /_all` and `DELETE /*` outright, regardless of what any client sends. This is defence in depth, not a substitute for step 1, but it turns a broad-target request into a 400 instead of an outcome.

Here is a concrete example showing input validation for a multi-tenant application:

```go
// WRONG: user input flows directly into a destructive operation.
// A user supplying "*" would delete all indices.
func handleDeleteIndex(userInput string) error {
    _, err := client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
        Index: []string{userInput},
    })
    return err
}
```

```go
// CORRECT: validate that the index name belongs to the tenant's namespace
// and does not contain the multi-target wildcard.
func handleDeleteIndex(tenantID, userInput string) error {
    if strings.ContainsRune(userInput, '*') {
        return fmt.Errorf("wildcard characters are not permitted in index names")
    }
    expected := fmt.Sprintf("tenant-%s-", tenantID)
    if !strings.HasPrefix(userInput, expected) {
        return fmt.Errorf("index %q is not in tenant namespace", userInput)
    }
    _, err := client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
        Index: []string{userInput},
    })
    return err
}
```

### A Reusable Validation Helper

A small helper that encodes "this value is a literal index name, not a pattern" makes the intent visible at every call site:

```go
package osx

import (
    "fmt"
    "strings"
)

// patternMeta are the characters and tokens OpenSearch interprets as
// multi-target operators rather than literal name characters.
var patternMeta = []string{"*", ",", "<", ">"}

// LiteralIndex returns name unchanged if it is a plausible single-index
// literal, or an error if it contains multi-target operators or matches a
// reserved pattern. Callers that intend to pass a pattern should not route
// the value through this function.
func LiteralIndex(name string) (string, error) {
    if name == "" {
        return "", fmt.Errorf("index name is empty")
    }
    if name == "_all" {
        return "", fmt.Errorf("index name %q is the reserved all-indices alias", name)
    }
    // A leading "-" is the exclusion operator inside multi-target
    // expressions, and is independently invalid as the first character of
    // a real index name.
    if strings.HasPrefix(name, "-") {
        return "", fmt.Errorf("index name %q has a leading exclusion operator", name)
    }
    for _, m := range patternMeta {
        if strings.Contains(name, m) {
            return "", fmt.Errorf("index name %q contains pattern character %q", name, m)
        }
    }
    return name, nil
}
```

Used at the call site:

```go
idx, err := osx.LiteralIndex(req.TenantIndex)
if err != nil {
    return fmt.Errorf("invalid tenant index: %w", err)
}
_, err = client.Indices.Delete(ctx, &opensearchapi.IndicesDeleteReq{
    Index: []string{idx},
})
```

The helper is deliberately strict. If a particular call site is _designed_ to accept patterns (an admin "purge by prefix" endpoint, for example), skip the helper there, and make that decision visible in the code rather than ambient.

### Be Explicit When You Want a Pattern

The inverse case (your code constructs a pattern on purpose) benefits from the same explicitness:

- Build the pattern in code from a validated literal, rather than accepting a pre-built pattern from outside:

  ```go
  // Caller controls the wildcard; only the tenant slug came from outside.
  slug, err := osx.LiteralIndex(tenantSlug)
  if err != nil {
      return err
  }
  pattern := slug + "-*"
  ```

- Pair the request with the narrowest `ExpandWildcards` value that does what you need (`open`, `closed`, `hidden`, `none`). `none` disables wildcard expansion entirely for that request, which is a useful belt-and-braces option on read paths where you expect an exact name:

  ```go
  // Restrict wildcard expansion to open indices only (exclude closed and hidden).
  resp, err := client.Search(ctx, &opensearchapi.SearchReq{
      Index: []string{"logs-*"},
      Body:  &opensearchapi.SearchBody{
          Query: &opensearchapi.CommonQueryDSLQueryContainer{
              MatchAll: &opensearchapi.CommonQueryDSLMatchAllQuery{},
          },
      },
      Params: &opensearchapi.SearchParams{
          ExpandWildcards: []string{"open"},
      },
  })
  ```

- For destructive multi-target operations, prefer two steps: resolve the pattern with a read API first (for example `CatIndicesReq` or `IndicesResolveIndexReq`), inspect or log the concrete target list, then act on that list. This trades one round trip for an auditable record of exactly which indices were affected.

### Document IDs and Other Path Parameters

The same encoding rules apply to every caller-supplied path segment, not just index names. Document IDs, template names, pipeline IDs and snapshot names are all percent-encoded for URL structure and otherwise passed through. Document IDs in particular are often derived from external data (a primary key, a message ID); the client will deliver `../_search` safely as the literal ID `%2E%2E%2F_search`, but if your IDs are supposed to be UUIDs, validating that on the way in is still the cheaper and clearer place to catch a mismatch.

### Restricting Wildcards (Planned)

A future version of the client will make literal-by-default the library's own behavior for path parameters: `*`, `,` and the other multi-target operators would be percent-encoded unless the caller opts in per request (for example via an `AllowIndexPatterns` field on the request struct, or by wrapping the value in a `Pattern(...)` constructor). That moves the explicit "I intend this to be a pattern" signal from application helpers like `LiteralIndex` above into the request type itself, so the safe path is the path of least resistance.

```go
// Future API (illustrative, not yet implemented):
//
// This will fail by default because the index pattern contains a wildcard:
//   _, err := client.Indices.Delete(ctx, req)
//   // err: "wildcard '*' in index name requires AllowWildcards option"
//
// Opt in when you've validated the pattern yourself:
//   _, err := client.Indices.Delete(ctx, req, opensearchapi.AllowWildcards())
```

Until this is available, applications that accept index names from external input should validate those names before passing them to the client, as shown in the examples above.

## Request Body Construction

The `v5preview/opensearchapi` package provides typed `Body` structs for most operations. Using the typed struct is the safest approach because the compiler enforces the schema and `json.Marshal` handles escaping automatically. String interpolation into JSON is a security risk because unescaped input can alter the structure of the request.

```go
// WRONG: string interpolation into a raw body reader. A search term
// containing a double quote breaks out of the JSON string and can
// alter the query structure.
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index:      []string{"products"},
    BodyReader: strings.NewReader(fmt.Sprintf(
        `{"query":{"match":{"title":"%s"}}}`, userQuery,
    )),
})
```

```go
// CORRECT: use the typed Body struct. The compiler enforces the schema
// and json.Marshal escapes all values.
//
// CommonQueryDSLQueryContainerMatchValue is a discriminated union that
// can be decoded but not constructed by external callers, so this
// example uses MatchPhrase (a plain map[string]string) for the same
// "search a field with user input" intent. Reach for BodyReader +
// opensearchutil.NewJSONReader (below) when only the union-shaped
// `match` form fits the query you need.
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body: &opensearchapi.SearchBody{
        Query: &opensearchapi.CommonQueryDSLQueryContainer{
            MatchPhrase: map[string]string{
                "title": userQuery,
            },
        },
    },
})
```

When you need to send a query shape that the typed structs do not cover, fall back to `BodyReader` with `json.Marshal` or `opensearchutil.NewJSONReader` for safe serialization:

```go
// ACCEPTABLE: BodyReader with structured serialization for queries
// not yet covered by typed Body structs.
body := opensearchutil.NewJSONReader(map[string]any{
    "query": map[string]any{
        "match": map[string]any{
            "title": userQuery,
        },
    },
})
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index:      []string{"products"},
    BodyReader: body,
})
```

On the response side, use the typed response fields for structured access. When you need the raw bytes (logging, proxying, custom deserialization), use `RawBody()`:

```go
resp, err := client.Search(ctx, &opensearchapi.SearchReq{
    Index: []string{"products"},
    Body:  &opensearchapi.SearchBody{Query: query},
})
if err != nil {
    return err
}

// Typed access (preferred):
for _, hit := range resp.Hits.Hits {
    fmt.Printf("score=%.2f index=%s id=%s\n", *hit.Score, hit.Index, hit.ID)
}

// Raw access (logging, proxying, debugging):
raw, err := io.ReadAll(resp.RawBody())
if err != nil {
    return err
}
log.Printf("raw response: %s", raw)
```

## Error Handling and Information Disclosure

OpenSearch error responses can contain internal details: index names, shard IDs, node addresses, stack traces, and query fragments. When surfacing errors to end users (HTTP responses, UI messages, logs accessible to non-operators), filter out internal details.

```go
// WRONG: returning the raw OpenSearch error to an end user.
// May expose internal index names, node topology, or query structure.
_, err := client.Search(ctx, searchReq)
if err != nil {
    http.Error(w, err.Error(), http.StatusInternalServerError)
    return
}
```

```go
// CORRECT: log the full error for operators; return a generic message to the user.
_, err := client.Search(ctx, searchReq)
if err != nil {
    log.Printf("search failed: %v", err)
    http.Error(w, "search request failed", http.StatusInternalServerError)
    return
}
```

For structured error inspection without string parsing, see the [Error Handling](error_handling.md) guide.

## Network and Transport Configuration

### Connection Timeouts

Always configure request timeouts. Without them, a slow or unresponsive cluster can cause goroutines to block indefinitely, exhausting connection pools and memory.

```go
client, err := opensearchapi.NewClient(
    opensearchapi.Config{
        Client: opensearch.Config{
            Addresses: []string{"https://opensearch.internal:9200"},
            Transport: func() http.RoundTripper {
                tp := http.DefaultTransport.(*http.Transport).Clone()
                tp.ResponseHeaderTimeout = 30 * time.Second
                return tp
            }(),
        },
    },
)
```

Or use context deadlines per-request:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()
resp, err := client.Search(ctx, searchReq)
```

### Custom Transports

When providing a custom `http.Transport`, always clone `http.DefaultTransport` first. A bare `&http.Transport{}` disables HTTP/2, sets `MaxIdleConnsPerHost` to 2, and removes all dialer timeouts. Under concurrency this causes excessive TLS handshakes and connection churn. See [Custom Transport](../USER_GUIDE.md#custom-transport) for details.

### Node Discovery

When `DiscoverNodesOnStart` or `DiscoverNodesInterval` is enabled, the client queries the cluster for its node topology. This is useful for load balancing but means the client trusts the addresses returned by the cluster. In environments where the cluster network is segmented from the application network, verify that discovered node addresses are reachable and expected.

## Quick Reference

| Practice                                                                | Risk if Omitted                                     |
| ----------------------------------------------------------------------- | --------------------------------------------------- |
| Use TLS with CA verification (`CACert`)                                 | Credential theft, data interception                 |
| Load credentials from environment or secrets manager                    | Credential exposure in source control and logs      |
| Validate user-supplied index names before use                           | Unintended cross-index operations via wildcards     |
| Use typed `Body` structs; fall back to `BodyReader` with `json.Marshal` | JSON injection, request structure manipulation      |
| Filter error details before returning to end users                      | Internal topology and query disclosure              |
| Set request timeouts (transport or context)                             | Resource exhaustion from slow/unresponsive clusters |
| Clone `http.DefaultTransport` for custom transports                     | Connection pool exhaustion, disabled HTTP/2         |
| Review `ExpandWildcards` on multi-index operations                      | Operations affecting more indices than intended     |

| Concern                                               | Client Behavior                                                            | Recommended Application Behavior                                                 |
| ----------------------------------------------------- | -------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| URL structure (`/`, `?`, `#`, `..`)                   | Always percent-encoded                                                     | Nothing extra required                                                           |
| Multi-target operators (`*`, `,`, `_all`, `-`, `< >`) | Passed through as-is                                                       | Validate as literal at the trust boundary; opt in to patterns explicitly in code |
| Destructive scope                                     | Per-request `expand_wildcards`; cluster `action.destructive_requires_name` | Enable both; resolve-then-act for bulk deletes                                   |
