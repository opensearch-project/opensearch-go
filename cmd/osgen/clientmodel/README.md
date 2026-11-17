# clientmodel

Reads the resolved client model (`opensearch-client-model.json`, format 0.2.0) that opensearch-api-specification builds from a bundled spec (`npm run model`). The model states every type's merged fields, requiredness, bases, union kind, constraints and version metadata, so a generator doesn't have to interpret `allOf`/`oneOf`/`anyOf`.

The model at the repository root is built from the vendored `opensearch-openapi.yaml`, so both describe the same types:

```
make gen-client-model SPEC_REPO=<checkout of opensearch-api-specification with the model tooling>
```

## Reading types from the model

`osgen api -model opensearch-client-model.json` (or `make gen-api GEN_FROM_MODEL=1`) takes every type from the model. `Lower` replaces the component schemas and the JSON bodies of the response and request body components with schemas built from the model, in one canonical form:

- an object is its own fields, plus one `allOf` `$ref` per base (osgen embeds it), plus one union member for a container or other union constraint;
- a union is a plain `oneOf`/`anyOf` of its members, with titles, version metadata and discriminator;
- version and deprecation metadata become the `x-version-*` extensions osgen reads.

Operations, paths and parameters still come from the OpenAPI file, and so do a few non-composition details the model doesn't carry: `x-type-name`, `x-enum-name` and the error wrapper extensions, and the descriptions of a union's inline branches.

The walker then renders that canonical form. Without `-model` nothing changes.

## Status: opt-in, not the default yet

With `-model`, the generated client builds and vets, and 4 generated files change (`types_gen.go`, `unions_gen.go`, and two security plugin files). The changes, all compatible with the hand-written code:

- **Fixes:**
  - Redeclared fields keep the base's requiredness, so five buckets fields (rare and multi/significant terms aggregates) and three neural `stat_type` fields are no longer pointers to required values. osgen's own tagshadow allowlist flagged them as "makes a required field optional".
  - `type: [number, string]` fields are typed unions instead of `json.RawMessage` (security `peer_certificates` ×2 and `ssl_openssl_version`: 3 new union types). `type: [null, object]` (`RollupsExplain.rollup_metadata`) is `map[string]json.RawMessage`.
  - A union branch that only restates a named type resolves to it. The suggest `completion` branch is `SearchCompletionSuggest` rather than a duplicate `SearchSuggestCompletion` type.
- **Equivalent:**
  - Fields are sorted together instead of by `allOf` member (`TermsAggregationFields`, `MetricAggregationBase`, `AggregationContainer`, `FunctionScoreContainer`, `ReplicationUpdateSettingsSettings`).
  - Type comments come from the schema's own description instead of a referencing branch's.
  - The always-empty `Void` buckets of the unmapped aggregates fall back to the embedded generic buckets.
- **New wrapper types:** `ExtendedBoundsDouble` and `ExtendedBoundsFieldDateMath` are emitted as embed-only wrappers instead of collapsing onto `ExtendedBounds`, because the model's merged requiredness re-requires `max`/`min`. The model can't tell whether the spec repeated `required` on the redeclaration.

Making `-model` the default is a decision about these public API changes.
