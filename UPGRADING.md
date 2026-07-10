# Upgrading OpenSearch Go Client

This is the version-history index for the OpenSearch Go client. Each entry below covers what changed in a given release line and what a caller must do when jumping to it. Pick the file matching the major version you are upgrading to.

| Upgrade target | Guide                                |
| -------------- | ------------------------------------ |
| `>= 5.0.0`     | [`UPGRADING_V5.md`](UPGRADING_V5.md) |
| `>= 4.0.0`     | [`UPGRADING_V4.md`](UPGRADING_V4.md) |
| `>= 3.0.0`     | [`UPGRADING_V3.md`](UPGRADING_V3.md) |
| `>= 2.3.0`     | [`UPGRADING_V2.md`](UPGRADING_V2.md) |

## v4 to v5 `opensearchapi/` surface delta

Upgrading from the hand-written v4 `opensearchapi/` package to the code-generated v5 surface is the largest single change in v5. [`UPGRADING_V5.md`](UPGRADING_V5.md) summarizes it; for the field-level delta (every rename, the `*Params` change, embedded `TimeoutParams`/`DebugParams`, `BulkResp.Items` becoming `[]BulkItem`) and the optional forward-compatible `replace` directive, see the deep-dive at [`opensearchapi/UPGRADING_V4_TO_V5.md`](opensearchapi/UPGRADING_V4_TO_V5.md).

The [`osapifix`](cmd/osapifix/README.md) tool automates most of this delta (import bump, type/method/field renames, value-to-pointer adjustments); see the [Automated migration](opensearchapi/UPGRADING_V4_TO_V5.md#automated-migration) section.

## Related references

- [`COMPATIBILITY.md`](COMPATIBILITY.md) - client/server version support matrix.
- [`opensearchapi/README.md`](opensearchapi/README.md) - everyday usage (errors, routing, response handling).
- [`guides/README.md`](guides/README.md) - task-oriented guides.
