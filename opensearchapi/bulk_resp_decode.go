// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// bulkRespGenericDecode is BulkResp without its methods, which the reflective
// decoder fills in.
type bulkRespGenericDecode BulkResp

// UnmarshalJSONFrom decodes a bulk response as the reflective decoder does, into
// the same values, without allocating each item's BulkRespItem and the values its
// pointer fields name one by one: those come from chunks shared by a run of
// items. The JSON codec the client decodes with calls it in place of the
// reflective decoder; callers that decode a BulkResp with encoding/json get the
// reflective decoder.
//
// Member names are matched as encoding/json matches them, ignoring case, and
// every value is decoded by the codec, so null, numbers and strings decode as
// they do anywhere else. A value of another shape than the bulk API gives, such
// as items that are not an array, is decoded by the reflective decoder.
// TestBulkRespDecoderKnowsEveryField fails when BulkResp, BulkItem or
// BulkRespItem gains a field this does not decode.
func (r *BulkResp) UnmarshalJSONFrom(dec *json.TextDecoder) error {
	if json.PeekKind(dec) != '{' {
		return json.UnmarshalDecode(dec, (*bulkRespGenericDecode)(r))
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	var store bulkRespStore
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		switch {
		case bytes.EqualFold(name, []byte("items")):
			err = r.decodeItems(dec, &store)
		case bytes.EqualFold(name, []byte("errors")):
			err = json.UnmarshalDecode(dec, &r.Errors)
		case bytes.EqualFold(name, []byte("took")):
			err = json.UnmarshalDecode(dec, &r.Took)
		case bytes.EqualFold(name, []byte("ingest_took")):
			err = json.UnmarshalDecode(dec, &r.IngestTook)
		default:
			err = dec.SkipValue()
		}
		if err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeItems decodes the items array into r.Items, reusing its capacity.
func (r *BulkResp) decodeItems(dec *json.TextDecoder, store *bulkRespStore) error {
	if json.PeekKind(dec) != '[' {
		return json.UnmarshalDecode(dec, &r.Items)
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	// An empty array decodes to an empty slice, not nil, as it does reflectively.
	if r.Items == nil {
		r.Items = []BulkItem{}
	}
	r.Items = r.Items[:0]
	for json.PeekKind(dec) != ']' {
		// Decoded in place: a local BulkItem would escape to the heap, through
		// the reflective fallback, once for every item.
		r.Items = append(r.Items, BulkItem{})
		if err := decodeBulkItem(dec, &r.Items[len(r.Items)-1], store); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeBulkItem decodes one item, {"<action>":{...}}, into item.
func decodeBulkItem(dec *json.TextDecoder, item *BulkItem, store *bulkRespStore) error {
	if json.PeekKind(dec) != '{' {
		return json.UnmarshalDecode(dec, item)
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		var action **BulkRespItem
		switch {
		case bytes.EqualFold(name, []byte("index")):
			action = &item.Index
		case bytes.EqualFold(name, []byte("create")):
			action = &item.Create
		case bytes.EqualFold(name, []byte("update")):
			action = &item.Update
		case bytes.EqualFold(name, []byte("delete")):
			action = &item.Delete
		default:
			if err := dec.SkipValue(); err != nil {
				return err
			}

			continue
		}
		if json.PeekKind(dec) != '{' {
			// null clears the action, and anything else fails as it does for the
			// reflective decoder.
			if err := json.UnmarshalDecode(dec, action); err != nil {
				return err
			}

			continue
		}
		slot := store.next()
		if err := slot.decode(dec); err != nil {
			return err
		}
		*action = &slot.item
	}
	_, err := dec.ReadToken()

	return err
}

// bulkRespSlot holds one action's result and the values its pointer fields name.
type bulkRespSlot struct {
	item                        BulkRespItem
	id, docType, result         string
	primaryTerm, seqNo, version int64
	forcedRefresh               bool
	shards                      ShardInfo
}

// decode decodes an action's result object into s.item.
func (s *bulkRespSlot) decode(dec *json.TextDecoder) error {
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	it := &s.item
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		switch {
		case bytes.EqualFold(name, []byte("_id")):
			err = decodeOptional(dec, &s.id, &it.ID)
		case bytes.EqualFold(name, []byte("_index")):
			err = json.UnmarshalDecode(dec, &it.Index)
		case bytes.EqualFold(name, []byte("status")):
			err = json.UnmarshalDecode(dec, &it.Status)
		case bytes.EqualFold(name, []byte("result")):
			err = decodeOptional(dec, &s.result, &it.Result)
		case bytes.EqualFold(name, []byte("_version")):
			err = decodeOptional(dec, &s.version, &it.Version)
		case bytes.EqualFold(name, []byte("_seq_no")):
			err = decodeOptional(dec, &s.seqNo, &it.SeqNo)
		case bytes.EqualFold(name, []byte("_primary_term")):
			err = decodeOptional(dec, &s.primaryTerm, &it.PrimaryTerm)
		case bytes.EqualFold(name, []byte("_shards")):
			err = decodeOptional(dec, &s.shards, &it.Shards)
		case bytes.EqualFold(name, []byte("_type")):
			err = decodeOptional(dec, &s.docType, &it.Type)
		case bytes.EqualFold(name, []byte("forced_refresh")):
			err = decodeOptional(dec, &s.forcedRefresh, &it.ForcedRefresh)
		case bytes.EqualFold(name, []byte("error")):
			err = json.UnmarshalDecode(dec, &it.Error)
		case bytes.EqualFold(name, []byte("get")):
			err = json.UnmarshalDecode(dec, &it.Get)
		default:
			err = dec.SkipValue()
		}
		if err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeOptional decodes the next value into dst and points field at it, as the
// reflective decoder fills a pointer field, or clears field for null.
func decodeOptional[T any](dec *json.TextDecoder, dst *T, field **T) error {
	if json.PeekKind(dec) == 'n' {
		*field = nil
		_, err := dec.ReadToken()

		return err
	}
	if err := json.UnmarshalDecode(dec, dst); err != nil {
		return err
	}
	*field = dst

	return nil
}

// bulkRespStore hands out bulkRespSlots from chunks that double in size up to a
// bound, so that a response costs a few allocations however many items it has,
// and a small one does not pay for a large chunk. A chunk is never grown, so a
// pointer into it stays valid.
type bulkRespStore struct {
	chunk []bulkRespSlot
}

const (
	bulkRespFirstChunk = 8
	bulkRespMaxChunk   = 256
)

func (st *bulkRespStore) next() *bulkRespSlot {
	if len(st.chunk) == cap(st.chunk) {
		size := min(max(2*cap(st.chunk), bulkRespFirstChunk), bulkRespMaxChunk)
		st.chunk = make([]bulkRespSlot, 0, size)
	}
	st.chunk = st.chunk[:len(st.chunk)+1]

	return &st.chunk[len(st.chunk)-1]
}

// memberName reads an object member's name and returns it unquoted. The name is
// the decoder's own bytes, good until its next read, unless it holds an escape,
// which is decoded into a copy.
func memberName(dec *json.TextDecoder) ([]byte, error) {
	v, err := dec.ReadValue()
	if err != nil {
		return nil, err
	}
	name := v[1 : len(v)-1]
	if bytes.IndexByte(name, '\\') < 0 {
		return name, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return nil, err
	}

	return []byte(s), nil
}
