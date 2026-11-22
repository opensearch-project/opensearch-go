// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// A terms query names its field as a member of the query object, next to
// boost and _name, so the field and its values live in TermsQuery.Terms.
func TestTermsQueryFields(t *testing.T) {
	t.Parallel()

	boost := float32(1.5)
	q := opensearchapi.CommonQueryDSLQueryContainer{Terms: &opensearchapi.CommonQueryDSLTermsQuery{
		Boost: &boost,
		Terms: map[string]opensearchapi.CommonQueryDSLTermsQueryField{
			"status": opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray([]opensearchapi.FieldValue{
				opensearchapi.NewFieldValueFromString("active"),
				opensearchapi.NewFieldValueFromString("pending"),
			}),
		},
	}}
	const want = `{"terms":{"boost":1.5,"status":["active","pending"]}}`

	got, err := json.Marshal(q)
	require.NoError(t, err)
	require.JSONEq(t, want, string(got))

	var decoded opensearchapi.CommonQueryDSLQueryContainer
	require.NoError(t, json.Unmarshal([]byte(want), &decoded))
	require.Equal(t, &boost, decoded.Terms.Boost)
	require.Len(t, decoded.Terms.Terms, 1)
	status := decoded.Terms.Terms["status"]
	values, err := status.Array()
	require.NoError(t, err)
	require.Len(t, values, 2)

	again, err := json.Marshal(decoded)
	require.NoError(t, err)
	require.JSONEq(t, want, string(again))
}

func TestTermsQueryLookup(t *testing.T) {
	t.Parallel()

	const in = `{"user":{"index":"users","id":"2","path":"followers"}}`
	var q opensearchapi.CommonQueryDSLTermsQuery
	require.NoError(t, json.Unmarshal([]byte(in), &q))
	user := q.Terms["user"]
	lookup, err := user.Lookup()
	require.NoError(t, err)
	require.Equal(t, "users", lookup.Index)

	got, err := json.Marshal(q)
	require.NoError(t, err)
	require.JSONEq(t, in, string(got))
}

func TestTermsQueryDeclaredKeyInTerms(t *testing.T) {
	t.Parallel()

	q := opensearchapi.CommonQueryDSLTermsQuery{Terms: map[string]opensearchapi.CommonQueryDSLTermsQueryField{
		"boost": opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray(nil),
	}}
	_, err := json.Marshal(q)
	require.ErrorContains(t, err, `Terms key "boost" is a declared field`)
}

func TestGeoDistanceSortLocation(t *testing.T) {
	t.Parallel()

	const in = `{"_geo_distance":{"order":"asc","unit":"km","pin.location":[[-70,40]]}}`
	var s opensearchapi.SortOptions
	require.NoError(t, json.Unmarshal([]byte(in), &s))
	require.Len(t, s.GeoDistance.Location["pin.location"], 1)

	got, err := json.Marshal(s)
	require.NoError(t, err)
	require.JSONEq(t, in, string(got))
}
