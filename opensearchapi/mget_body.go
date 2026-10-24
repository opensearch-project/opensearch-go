// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"
	"strconv"

	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// encodeMGetBody encodes the operations at slots as an mget body, {"docs":[...]},
// in a pooled buffer, writing the documents as the generated request's encoder
// would. The caller holds the owner's reference, and passes the body to
// bufbody.Release once the request it was sent in has returned.
func encodeMGetBody(ops []MGetOperation, slots []int) (*bufbody.Pooled, error) {
	return bufbody.NewPooled(func(buf *bytes.Buffer) error {
		// Each document is encoded on its own, straight from the caller's request,
		// rather than copied into an MGetBody to encode in one call, which would
		// allocate the copy. A document appendMGetOperation leaves to the encoder
		// costs the body one encoder; the encoder follows the document with a
		// newline, which is whitespace between array elements.
		var enc *json.Encoder
		buf.WriteString(`{"docs":[`)
		for i, pos := range slots {
			if i > 0 {
				buf.WriteByte(',')
			}
			if b, ok := appendMGetOperation(buf.AvailableBuffer(), &ops[pos]); ok {
				buf.Write(b)
				continue
			}
			if enc == nil {
				enc = json.NewEncoder(buf)
			}
			if err := enc.Encode(&ops[pos]); err != nil {
				return err
			}
		}
		buf.WriteString(`]}`)

		return nil
	})
}

// appendMGetOperation appends op to b as encoding/json encodes it, byte for
// byte. It reports false, and appends nothing, for an op it leaves to the
// encoder: one with a _source, or with a string the encoder would escape.
// TestAppendMGetOperationKnowsEveryField fails when MGetOperation gains a field
// this does not write.
func appendMGetOperation(b []byte, op *MGetOperation) ([]byte, bool) {
	if op.Source != nil {
		return b, false
	}
	start := len(b)
	b = append(b, `{"_id":`...)
	b, ok := json.AppendString(b, op.ID, true)
	if ok && op.Index != nil {
		b = append(b, `,"_index":`...)
		b, ok = json.AppendString(b, *op.Index, true)
	}
	if ok && op.Routing != nil {
		b = append(b, `,"routing":`...)
		b, ok = json.AppendString(b, *op.Routing, true)
	}
	if ok && op.StoredFields != nil {
		b = append(b, `,"stored_fields":`...)
		b, ok = json.AppendString(b, *op.StoredFields, true)
	}
	if ok && op.Version != nil {
		b = append(b, `,"version":`...)
		b = strconv.AppendInt(b, *op.Version, 10)
	}
	if ok && op.VersionType != nil {
		b = append(b, `,"version_type":`...)
		b, ok = json.AppendString(b, string(*op.VersionType), true)
	}
	if !ok {
		return b[:start], false
	}

	return append(b, '}'), true
}
