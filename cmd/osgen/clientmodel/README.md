# clientmodel

Reads the resolved client model (`opensearch-client-model.json`, format 0.2.0) that
opensearch-api-specification builds from a bundled spec (`npm run model`). The model
states every type's merged fields, requiredness, union kind and constraints, so a
generator doesn't have to interpret `allOf`/`oneOf`/`anyOf` itself.

The model at the repository root is built from the vendored `opensearch-openapi.yaml`,
so both describe the same types:

```
make gen-client-model SPEC_REPO=<checkout of opensearch-api-specification with the model tooling>
```

## Status

The package is the seam for osgen's type input. The walker (`resp_walk.go` and the files
around it) still reads the OpenAPI file. `TestClientModelAgreesOnTaggedUnions` checks the
model against osgen's own reading: all 8 discriminated unions of the vendored spec match
(same property, default, and tag per branch, in order).

Switching the walker to the model without changing the generated API requires the model
to carry two things it drops today:

1. **Version and deprecation metadata** on types, fields and union branches:
   `x-version-added`, `x-version-removed`, `x-version-deprecated`,
   `x-deprecation-message`. osgen filters by them (`-min-version`, `-max-version`,
   `-remove-deprecated`), emits breadcrumbs for what it excludes, and writes deprecation
   comments. The vendored spec has them on about 147 properties, 15 union branches and 29
   schemas.
2. **`allOf` bases.** osgen embeds each `allOf` `$ref` member as a Go struct
   (`IsEmbed`, e.g. `CommonAggregationsAggregation` inside
   `CommonAggregationsAggregationContainer`); about 452 named schemas do this. The model
   flattens bases into `fields`, so a reader would emit flat structs, a breaking change to
   the public Go API. The model would need to list the bases (for example
   `bases: [ref, …]` next to the merged `fields`, with each field marking which base
   declares it).

Smaller items, which the walker can keep reading from the OpenAPI file because they are not
composition: `x-type-name` and `x-enum-name` (naming), `x-error-status` and
`x-error-root-cause-type` on error wrapper schemas, and operations, paths and parameters.
