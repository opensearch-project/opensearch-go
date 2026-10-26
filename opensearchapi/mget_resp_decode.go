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

// mgetRespGenericDecode is MGetResp without its methods, which the reflective
// decoder fills in.
type mgetRespGenericDecode MGetResp

// UnmarshalJSONFrom decodes an mget response as the reflective decoder does, with
// the generated MGetRespItem.UnmarshalJSONFrom for each document, into the same
// values, without allocating each document's GetResult and the values its
// pointer fields name one by one: those come from chunks shared by a run of
// documents. Docs keeps the capacity it has, which the mget dispatch sizes for
// the documents asked for. The JSON codec the client decodes with calls it in
// place of the reflective decoder; callers that decode an MGetResp with
// encoding/json get the reflective decoder.
//
// A document's GetResult shares its chunk with the documents decoded beside it,
// so a GetResult kept past the response keeps the chunk too; MGetRespItem.RawJSON
// already keeps the whole response body. Source is a copy, as it is when decoded
// reflectively.
//
// Member names are matched as encoding/json matches them, ignoring case, and
// every value is decoded by the codec. A document that is not an object, or a
// value of another shape than the mget API gives, is decoded as the generated
// decoder decodes it. TestMGetRespDecoderKnowsEveryField and
// TestMGetRespDecoderKnowsTheUnion fail when MGetResp, GetResult or MGetRespItem
// changes in a way this does not decode.
func (r *MGetResp) UnmarshalJSONFrom(dec *json.TextDecoder) error {
	if json.PeekKind(dec) != '{' {
		return json.UnmarshalDecode(dec, (*mgetRespGenericDecode)(r))
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		if bytes.EqualFold(name, []byte("docs")) {
			err = r.decodeDocs(dec)
		} else {
			err = dec.SkipValue()
		}
		if err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeDocs decodes the docs array into r.Docs, reusing its capacity.
func (r *MGetResp) decodeDocs(dec *json.TextDecoder) error {
	if json.PeekKind(dec) != '[' {
		return json.UnmarshalDecode(dec, &r.Docs)
	}
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	// An empty array decodes to an empty slice, not nil, as it does reflectively.
	if r.Docs == nil {
		r.Docs = []MGetRespItem{}
	}
	r.Docs = r.Docs[:0]
	store := mgetDocStore{first: cap(r.Docs)}
	for json.PeekKind(dec) != ']' {
		// Decoded in place: a local MGetRespItem would escape to the heap, through
		// the generated fallback, once for every document.
		r.Docs = append(r.Docs, MGetRespItem{})
		if err := decodeMGetDoc(dec, &r.Docs[len(r.Docs)-1], &store); err != nil {
			return err
		}
	}
	_, err := dec.ReadToken()

	return err
}

// decodeMGetDoc decodes one document into u, as MGetRespItem.UnmarshalJSONFrom
// does: a document with an error member is a MultiGetError, any other object a
// GetResult, and RawJSON is the bytes the document was read from.
func decodeMGetDoc(dec *json.TextDecoder, u *MGetRespItem, store *mgetDocStore) error {
	if json.PeekKind(dec) != '{' {
		return u.UnmarshalJSONFrom(dec)
	}
	mark := json.MarkOf(dec)
	slot := store.next()
	if err := slot.decode(dec); err != nil {
		return err
	}
	u.raw = mark.Consumed(dec)
	if len(slot.errMember) == 0 {
		u.typ = MGetRespItemGetResultType
		u.value = &slot.get

		return nil
	}

	var v MGetMultiGetError
	if json.Valid(u.raw) {
		if err := json.Unmarshal(u.raw, &v); err != nil {
			return err
		}
	} else {
		// A decoder reading an io.Reader reuses its buffer, so the bytes are gone or
		// overwritten; build the branch from the members read instead.
		u.raw = nil
		v.ID, v.Index = slot.get.ID, slot.get.Index
		if err := json.Unmarshal(slot.errMember, &v.Error); err != nil {
			return err
		}
	}
	u.typ = MGetRespItemMultiGetErrorType
	u.value = &v

	return nil
}

// mgetDocSlot holds one document's GetResult and the values its pointer fields
// name.
type mgetDocSlot struct {
	get                         GetResult
	routing, docType            string
	primaryTerm, seqNo, version int64
	// errMember is the document's error member, which makes it a MultiGetError.
	// It is kept here, not in a local, which would escape to the heap for every
	// document.
	errMember json.RawMessage
}

// decode decodes a document object into s.get, and its error member, if it has
// one, into s.errMember.
func (s *mgetDocSlot) decode(dec *json.TextDecoder) error {
	if _, err := dec.ReadToken(); err != nil {
		return err
	}
	g := &s.get
	for json.PeekKind(dec) != '}' {
		name, err := memberName(dec)
		if err != nil {
			return err
		}
		switch {
		case bytes.EqualFold(name, []byte("_id")):
			err = json.UnmarshalDecode(dec, &g.ID)
		case bytes.EqualFold(name, []byte("_index")):
			err = json.UnmarshalDecode(dec, &g.Index)
		case bytes.EqualFold(name, []byte("found")):
			err = json.UnmarshalDecode(dec, &g.Found)
		case bytes.EqualFold(name, []byte("_source")):
			err = json.UnmarshalDecode(dec, &g.Source)
		case bytes.EqualFold(name, []byte("_version")):
			err = decodeOptional(dec, &s.version, &g.Version)
		case bytes.EqualFold(name, []byte("_seq_no")):
			err = decodeOptional(dec, &s.seqNo, &g.SeqNo)
		case bytes.EqualFold(name, []byte("_primary_term")):
			err = decodeOptional(dec, &s.primaryTerm, &g.PrimaryTerm)
		case bytes.EqualFold(name, []byte("_routing")):
			err = decodeOptional(dec, &s.routing, &g.Routing)
		case bytes.EqualFold(name, []byte("_type")):
			err = decodeOptional(dec, &s.docType, &g.Type)
		case bytes.EqualFold(name, []byte("fields")):
			err = json.UnmarshalDecode(dec, &g.Fields)
		case bytes.EqualFold(name, []byte("error")):
			err = json.UnmarshalDecode(dec, &s.errMember)
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

// mgetDocStore hands out mgetDocSlots from chunks: the first as large as first,
// the room Docs has for the documents asked for, else small, and each after it
// twice the last up to a bound. A chunk is never grown, so a pointer into it
// stays valid.
type mgetDocStore struct {
	first int
	chunk []mgetDocSlot
}

const (
	mgetDocFirstChunk = 8
	mgetDocMaxChunk   = 256
)

func (st *mgetDocStore) next() *mgetDocSlot {
	if len(st.chunk) == cap(st.chunk) {
		size := min(max(2*cap(st.chunk), mgetDocFirstChunk), mgetDocMaxChunk)
		if st.chunk == nil && st.first > 0 {
			size = st.first
		}
		st.chunk = make([]mgetDocSlot, 0, size)
	}
	st.chunk = st.chunk[:len(st.chunk)+1]

	return &st.chunk[len(st.chunk)-1]
}
