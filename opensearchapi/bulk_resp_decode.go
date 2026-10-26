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
// items. Items keeps the capacity it has, which the bulk dispatch sizes for the
// actions the bulk indexer sends. The JSON codec the client decodes with calls it
// in place of the reflective decoder, as encoding/json does where it runs on the
// v2 engine, from Go 1.27.
//
// Member names are matched as encoding/json matches them, ignoring case, and
// every value is decoded by the codec, so null, numbers and strings decode as
// they do anywhere else. A repeated member, or an item already in Items, is
// merged into as the reflective decoder merges into it. A value of the wrong
// type is left unset and the decode goes on, returning the first such error,
// naming BulkResp, once the response has been read. A value of another shape
// than the bulk API gives, such as items that are not an array, is decoded by
// the reflective decoder.
// TestBulkRespDecoderKnowsEveryField fails when BulkResp, BulkItem or
// BulkRespItem gains a field this does not decode.
func (r *BulkResp) UnmarshalJSONFrom(dec *json.TextDecoder) error {
	var errs typeErrors
	if err := errs.keep(r.decode(dec, &errs)); err != nil {
		return err
	}

	return errs.err("BulkResp")
}

func (r *BulkResp) decode(dec *json.TextDecoder, errs *typeErrors) error {
	if json.PeekKind(dec) != '{' {
		return json.UnmarshalDecode(dec, (*bulkRespGenericDecode)(r))
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	store := chunkStore[bulkRespSlot]{first: cap(r.Items)}
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		switch {
		case bytes.EqualFold(name, []byte("items")):
			err = r.decodeItems(dec, &store, errs)
		case bytes.EqualFold(name, []byte("errors")):
			err = json.UnmarshalDecode(dec, &r.Errors)
		case bytes.EqualFold(name, []byte("took")):
			err = json.UnmarshalDecode(dec, &r.Took)
		case bytes.EqualFold(name, []byte("ingest_took")):
			err = json.UnmarshalDecode(dec, &r.IngestTook)
		default:
			err = dec.SkipValue()
		}
		if err = errs.keep(err); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeItems decodes the items array into r.Items, reusing its capacity.
func (r *BulkResp) decodeItems(dec *json.TextDecoder, store *chunkStore[bulkRespSlot], errs *typeErrors) error {
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
		// Decoded in place, merging into what the capacity already holds as the
		// reflective decoder does: a local BulkItem would escape to the heap,
		// through the reflective fallback, once for every item.
		if len(r.Items) < cap(r.Items) {
			r.Items = r.Items[:len(r.Items)+1]
		} else {
			r.Items = append(r.Items, BulkItem{})
		}
		if err := decodeBulkItem(dec, &r.Items[len(r.Items)-1], store, errs); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeBulkItem decodes one item, {"<action>":{...}}, into item.
func decodeBulkItem(dec *json.TextDecoder, item *BulkItem, store *chunkStore[bulkRespSlot], errs *typeErrors) error {
	if json.PeekKind(dec) != '{' {
		return errs.keep(json.UnmarshalDecode(dec, item))
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
			if err := errs.keep(json.UnmarshalDecode(dec, action)); err != nil {
				return err
			}

			continue
		}
		slot := store.next()
		// A repeated action merges into the result the first one decoded.
		if *action == nil {
			*action = &slot.item
		}
		if err := slot.decode(dec, *action, errs); err != nil {
			return err
		}
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

// decode decodes an action's result object into it: s.item, or the result of an
// earlier member for the same action, which it merges into. Pointer fields that
// are nil are pointed at s's storage.
func (s *bulkRespSlot) decode(dec *json.TextDecoder, it *BulkRespItem, errs *typeErrors) error {
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
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
		if err = errs.keep(err); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}
