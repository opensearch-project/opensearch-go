// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	json "github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
)

// MSearchSubSearch is one search of a multi-search request. It is not a
// SearchReq because SearchReq's BodyReader, Header and Params have no meaning
// on a single line of an msearch body.
type MSearchSubSearch struct {
	// Indices the search runs against. When empty, the search runs against
	// the indices of the MSearchReq that carries the body.
	Indices []string

	// Body is the search definition. A nil Body searches with the default
	// match-all query.
	Body *SearchBody
}

// BuildMSearchBody encodes searches as the newline-delimited JSON body that
// MSearchReq.Body expects: a header line, then a search body line, for each
// sub-search, each terminated by a newline. The server answers in the order
// of searches. It returns an error when searches is empty or a body does not
// marshal.
func BuildMSearchBody(searches ...MSearchSubSearch) (io.Reader, error) {
	if len(searches) == 0 {
		return nil, errors.New("opensearchapi: msearch body requires at least one sub-search")
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for i, s := range searches {
		header := struct {
			Index []string `json:"index,omitempty"`
		}{Index: s.Indices}
		if err := enc.Encode(header); err != nil {
			return nil, fmt.Errorf("opensearchapi: encoding header of sub-search %d: %w", i, err)
		}

		if s.Body == nil {
			buf.WriteString("{}\n")
			continue
		}
		if err := enc.Encode(s.Body); err != nil {
			return nil, fmt.Errorf("opensearchapi: encoding body of sub-search %d: %w", i, err)
		}
	}

	return bytes.NewReader(buf.Bytes()), nil
}
