// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package errwrap catalogs the partial-failure wrapper schemas declared
// by OpenSearch operations.
//
// Wrapper names come from the spec's `x-error-responses` extension; the
// segment after the final `___` in each $ref is the canonical identifier
// (PascalCase, e.g. `BulkItems`). All downstream Go identifiers --
// errmask bit, env-var token (snake_case), catalog key, Resp method
// name -- are mechanically derived from that single name. See
// [errmask.Parse] for the case conversion.
//
// [OperationWrappers] is a fallback for operations not yet annotated
// upstream. The spec annotation wins where present.
package errwrap
