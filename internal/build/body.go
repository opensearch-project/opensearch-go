// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package build

import (
	"bytes"

	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// JSONBody returns v encoded as a request body, as encoding/json.Marshal
// encodes it, in a pooled buffer. The engine writes the encoding into the
// buffer directly, so it is held once. The caller releases the body with
// bufbody.Release once the request it was sent in has returned.
func JSONBody(v any) (*bufbody.Pooled, error) {
	return bufbody.NewPooled(func(buf *bytes.Buffer) error {
		return json.MarshalWrite(buf, v)
	})
}
