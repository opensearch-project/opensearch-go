// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/internal/jsonx"
	"github.com/opensearch-project/opensearch-go/v5/opensearchapi"
)

// A terms query whose field name is a JSON key next to fixed properties must
// carry that field through a marshal/unmarshal round trip.
func TestQueryContainerFieldKeyRoundTrip(t *testing.T) {
	t.Parallel()

	boost := float32(2)
	tests := []struct {
		name  string
		query opensearchapi.CommonQueryDSLQueryContainer
		want  string
		// check asserts the decoded union branch and values; the unions keep the
		// decoded bytes in an unexported field, so whole-struct Equal cannot match.
		check func(t *testing.T, back opensearchapi.CommonQueryDSLQueryContainer)
	}{
		{
			name: "terms values",
			query: opensearchapi.CommonQueryDSLQueryContainer{Terms: &opensearchapi.CommonQueryDSLTermsQuery{
				Boost: &boost,
				Fields: map[string]opensearchapi.CommonQueryDSLTermsQueryField{
					"user": opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray([]opensearchapi.FieldValue{
						opensearchapi.NewFieldValueFromString("a"),
						opensearchapi.NewFieldValueFromString("b"),
					}),
				},
			}},
			want: `{"terms":{"user":["a","b"],"boost":2}}`,
			check: func(t *testing.T, back opensearchapi.CommonQueryDSLQueryContainer) {
				t.Helper()
				require.Equal(t, &boost, back.Terms.Boost)
				user := back.Terms.Fields["user"]
				require.Equal(t, opensearchapi.CommonQueryDSLTermsQueryFieldArrayType, user.Type())
				vals, err := user.Array()
				require.NoError(t, err)
				got := make([]string, len(vals))
				for i := range vals {
					got[i], err = vals[i].String()
					require.NoError(t, err)
				}
				require.Equal(t, []string{"a", "b"}, got)
			},
		},
		{
			name: "terms lookup",
			query: opensearchapi.CommonQueryDSLQueryContainer{Terms: &opensearchapi.CommonQueryDSLTermsQuery{
				Fields: map[string]opensearchapi.CommonQueryDSLTermsQueryField{
					"user": opensearchapi.NewCommonQueryDSLTermsQueryFieldFromLookup(opensearchapi.CommonQueryDSLTermsLookup{
						Index: "i", ID: "1", Path: "p",
					}),
				},
			}},
			want: `{"terms":{"user":{"index":"i","id":"1","path":"p"}}}`,
			check: func(t *testing.T, back opensearchapi.CommonQueryDSLQueryContainer) {
				t.Helper()
				user := back.Terms.Fields["user"]
				require.Equal(t, opensearchapi.CommonQueryDSLTermsQueryFieldLookupType, user.Type())
				lookup, err := user.Lookup()
				require.NoError(t, err)
				require.Equal(t, opensearchapi.CommonQueryDSLTermsLookup{Index: "i", ID: "1", Path: "p"}, lookup)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.query)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))

			var back opensearchapi.CommonQueryDSLQueryContainer
			require.NoError(t, json.Unmarshal([]byte(tt.want), &back))
			again, err := json.Marshal(back)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(again))

			tt.check(t, back)
		})
	}
}

// Documented terms shapes decode and re-encode to the same JSON.
func TestQueryTermsShapesRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{"terms array", `{"terms":{"user.id":["kimchy","elkbee"],"boost":1.0}}`},
		{"terms mixed scalars", `{"terms":{"n":[1,2.5,true]}}`},
		{"terms lookup with routing", `{"terms":{"color":{"index":"my-index","id":"2","path":"color","routing":"r"}}}`},
		{"terms name and value_type", `{"terms":{"_name":"q","value_type":"bitmap","f":["a"]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var q opensearchapi.CommonQueryDSLQueryContainer
			require.NoError(t, json.Unmarshal([]byte(tt.json), &q))
			got, err := json.Marshal(q)
			require.NoError(t, err)
			require.JSONEq(t, tt.json, string(got))
		})
	}
}

// Field-keyed queries other than terms carry no typed field map; their field
// values are not decoded, but a request using any documented shape must still decode.
func TestQueryFieldKeyedShapesStillDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
	}{
		{"geo_distance geojson point", `{"geo_distance":{"distance":"12km","pin.location":{"type":"Point","coordinates":[-70,40]}}}`},
		{"geo_distance string lat lon", `{"geo_distance":{"distance":"12km","pin.location":{"lat":"40","lon":"-70"}}}`},
		{"geo_distance latlon", `{"geo_distance":{"distance":"200km","pin.location":{"lat":40,"lon":-70}}}`},
		{
			"geo_bounding_box corners",
			`{"geo_bounding_box":{"pin.location":{"top_left":{"lat":40.73,"lon":-74.1},"bottom_right":{"lat":40.01,"lon":-71.12}}}}`,
		},
		{"geo_polygon geojson points", `{"geo_polygon":{"person.location":{"points":[{"type":"Point","coordinates":[-70,40]}]}}}`},
		{"geo_shape wkt", `{"geo_shape":{"location":{"shape":"POINT (13 53)"}}}`},
		{"geo_shape indexed", `{"geo_shape":{"location":{"indexed_shape":{"index":"shapes","id":"deu","path":"location"}}}}`},
		{
			"geo_shape geometrycollection",
			`{"geo_shape":{"location":{"shape":{"type":"geometrycollection","geometries":[{"type":"point","coordinates":[1,2]}]}}}}`,
		},
		{"xy_shape wkt", `{"xy_shape":{"g":{"shape":"POINT (1 2)"}}}`},
		{"xy_shape circle", `{"xy_shape":{"g":{"shape":{"type":"circle","coordinates":[1,2],"radius":"5m"}}}}`},
		{"gauss epoch origin", `{"function_score":{"functions":[{"gauss":{"date":{"origin":1577836800000,"scale":"10d"}}}]}}`},
		{"gauss float origin", `{"function_score":{"functions":[{"gauss":{"date":{"origin":1.5e12,"scale":"10d"}}}]}}`},
		{"exp string origin", `{"function_score":{"functions":[{"exp":{"loc":{"origin":"40,-70","scale":"2km"}}}]}}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var q opensearchapi.CommonQueryDSLQueryContainer
			require.NoError(t, json.Unmarshal([]byte(tt.json), &q))
		})
	}
}

// Decoding sends a key that names a fixed property to that property, not to the
// map. The codec matches fixed keys case-insensitively, so the match ignores case.
func TestQueryFieldKeyDecodeShadowing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		json  string
		check func(t *testing.T, terms opensearchapi.CommonQueryDSLTermsQuery)
	}{
		{
			name: "boost",
			json: `{"boost":2,"user":["b"]}`,
			check: func(t *testing.T, terms opensearchapi.CommonQueryDSLTermsQuery) {
				t.Helper()
				require.NotNil(t, terms.Boost)
				require.InDelta(t, float32(2), *terms.Boost, 0)
			},
		},
		{
			name: "Boost",
			json: `{"Boost":2,"user":["b"]}`,
			check: func(t *testing.T, terms opensearchapi.CommonQueryDSLTermsQuery) {
				t.Helper()
				require.NotNil(t, terms.Boost)
				require.InDelta(t, float32(2), *terms.Boost, 0)
			},
		},
		{
			name: "_NAME",
			json: `{"_NAME":"q","user":["b"]}`,
			check: func(t *testing.T, terms opensearchapi.CommonQueryDSLTermsQuery) {
				t.Helper()
				require.NotNil(t, terms.Name)
				require.Equal(t, "q", *terms.Name)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var terms opensearchapi.CommonQueryDSLTermsQuery
			require.NoError(t, json.Unmarshal([]byte(tt.json), &terms))
			tt.check(t, terms)
			require.Len(t, terms.Fields, 1)
			require.Contains(t, terms.Fields, "user")
		})
	}
}

// Encoding rejects a map key that names a fixed property instead of dropping the
// entry. Keys match case-insensitively, as decoding matches them, even when the
// fixed field is unset.
func TestQueryFieldKeyNamingFixedPropertyFailsMarshal(t *testing.T) {
	t.Parallel()

	arr := func(s string) opensearchapi.CommonQueryDSLTermsQueryField {
		return opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray([]opensearchapi.FieldValue{opensearchapi.NewFieldValueFromString(s)})
	}
	tests := []struct {
		name    string
		key     string
		wantErr string
		want    string
	}{
		{name: "boost", key: "boost", wantErr: `key "boost" in Fields names a fixed property`},
		{name: "Boost", key: "Boost", wantErr: `key "Boost" in Fields names a fixed property`},
		{name: "BOOST", key: "BOOST", wantErr: `key "BOOST" in Fields names a fixed property`},
		{name: "non-colliding key", key: "user", want: `{"user":["a"],"other":["b"]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			terms := opensearchapi.CommonQueryDSLTermsQuery{Fields: map[string]opensearchapi.CommonQueryDSLTermsQueryField{
				tt.key:  arr("a"),
				"other": arr("b"),
			}}
			got, err := json.Marshal(terms)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, "CommonQueryDSLTermsQuery")
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

// Values the spec's types do not describe decode and re-encode with the same
// meaning. Each value's bytes are re-emitted as decoded; the enclosing object is
// re-encoded, so its keys come back sorted.
func TestQueryTermsBodiesRoundTripVerbatim(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		json string
		// want is the re-encoded body when it differs from json, which happens
		// only in key order.
		want string
	}{
		{"lookup by query", `{"terms":{"username":{"index":"groups","path":"members","query":{"term":{"group":"g1"}}}}}`, ""},
		{"lookup index only", `{"terms":{"f":{"index":"i"}}}`, ""},
		{"lookup numeric id", `{"terms":{"f":{"index":"i","id":2,"path":"p"}}}`, ""},
		{"scalar string", `{"terms":{"n":"a"}}`, ""},
		{"scalar number", `{"terms":{"n":5}}`, ""},
		{"scalar bool", `{"terms":{"n":true}}`, ""},
		{"nested array", `{"terms":{"n":[["a"]]}}`, ""},
		{"array of objects", `{"terms":{"n":[{"a":1}]}}`, ""},
		{"overflowing number", `{"terms":{"n":[1e400]}}`, ""},
		{"large integer", `{"terms":{"n":[12345678901234567890]}}`, ""},
		{
			"several keys",
			`{"terms":{"z":[12345678901234567890],"boost":2,"a":{"index":"i","query":{"match_all":{}}}}}`,
			`{"terms":{"a":{"index":"i","query":{"match_all":{}}},"boost":2,"z":[12345678901234567890]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var q opensearchapi.CommonQueryDSLQueryContainer
			require.NoError(t, json.Unmarshal([]byte(tt.json), &q))
			got, err := json.Marshal(q)
			require.NoError(t, err)
			want := tt.want
			if want == "" {
				want = tt.json
			}
			// Byte equality, because JSONEq cannot parse 1e400 and would round large
			// integers.
			require.Equal(t, want, string(got))
		})
	}
}

// Spec-shaped values still decode to their typed branch.
func TestQueryTermsTypedAccessAfterDecode(t *testing.T) {
	t.Parallel()

	t.Run("array", func(t *testing.T) {
		t.Parallel()

		var terms opensearchapi.CommonQueryDSLTermsQuery
		require.NoError(t, json.Unmarshal([]byte(`{"user":["a","b"]}`), &terms))
		user := terms.Fields["user"]
		require.Equal(t, opensearchapi.CommonQueryDSLTermsQueryFieldArrayType, user.Type())
		vals, err := user.Array()
		require.NoError(t, err)
		require.Len(t, vals, 2)
	})

	t.Run("lookup", func(t *testing.T) {
		t.Parallel()

		var terms opensearchapi.CommonQueryDSLTermsQuery
		require.NoError(t, json.Unmarshal([]byte(`{"user":{"index":"i","id":"1","path":"p","routing":"r"}}`), &terms))
		user := terms.Fields["user"]
		require.Equal(t, opensearchapi.CommonQueryDSLTermsQueryFieldLookupType, user.Type())
		lookup, err := user.Lookup()
		require.NoError(t, err)
		require.Equal(t, "i", lookup.Index)
		require.Equal(t, "1", lookup.ID)
		require.Equal(t, "p", lookup.Path)
	})
}

// A map entry replaced after decoding marshals as the new value, not the decoded bytes.
func TestQueryTermsReplaceAfterDecode(t *testing.T) {
	t.Parallel()

	var terms opensearchapi.CommonQueryDSLTermsQuery
	require.NoError(t, json.Unmarshal([]byte(`{"user":["a"]}`), &terms))
	terms.Fields["user"] = opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray([]opensearchapi.FieldValue{
		opensearchapi.NewFieldValueFromString("b"),
	})
	got, err := json.Marshal(terms)
	require.NoError(t, err)
	require.JSONEq(t, `{"user":["b"]}`, string(got))
}

// The terms-query value union, called directly rather than through its parent.
func TestQueryTermsQueryFieldUnion(t *testing.T) {
	t.Parallel()

	t.Run("type names", func(t *testing.T) {
		t.Parallel()

		require.Equal(t, "Array", opensearchapi.CommonQueryDSLTermsQueryFieldArrayType.String())
		require.Equal(t, "Lookup", opensearchapi.CommonQueryDSLTermsQueryFieldLookupType.String())
		require.Equal(t, "unknown", opensearchapi.CommonQueryDSLTermsQueryFieldUnknownType.String())
		require.Equal(t, "unknown", opensearchapi.CommonQueryDSLTermsQueryFieldType(99).String())
	})

	t.Run("accessors on the wrong branch", func(t *testing.T) {
		t.Parallel()

		arr := opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray(
			[]opensearchapi.FieldValue{opensearchapi.NewFieldValueFromString("a")})
		lookup := opensearchapi.NewCommonQueryDSLTermsQueryFieldFromLookup(
			opensearchapi.CommonQueryDSLTermsLookup{Index: "i", ID: "1", Path: "p"})
		var zero opensearchapi.CommonQueryDSLTermsQueryField

		_, err := arr.Lookup()
		var be *opensearchapi.UnionBranchError
		require.ErrorAs(t, err, &be)
		require.Equal(t, "Lookup", be.Want)
		require.Equal(t, "Array", be.Got)

		vals, err := lookup.Array()
		require.ErrorAs(t, err, &be)
		require.Equal(t, "Array", be.Want)
		require.Equal(t, "Lookup", be.Got)
		require.Nil(t, vals)

		vals, err = zero.Array()
		require.ErrorAs(t, err, &be)
		require.Equal(t, "unknown", be.Got)
		require.Nil(t, vals)

		l, err := zero.Lookup()
		require.ErrorAs(t, err, &be)
		require.Equal(t, "unknown", be.Got)
		require.Equal(t, opensearchapi.CommonQueryDSLTermsLookup{}, l)
	})

	t.Run("UnmarshalJSON", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			data    string
			want    opensearchapi.CommonQueryDSLTermsQueryFieldType
			wantErr string
		}{
			{name: "array", data: `["a",1]`, want: opensearchapi.CommonQueryDSLTermsQueryFieldArrayType},
			{name: "lookup", data: `{"index":"i","id":"1","path":"p"}`, want: opensearchapi.CommonQueryDSLTermsQueryFieldLookupType},
			{name: "null", data: `null`, want: opensearchapi.CommonQueryDSLTermsQueryFieldUnknownType},
			{name: "empty", data: ``, want: opensearchapi.CommonQueryDSLTermsQueryFieldUnknownType},
			{name: "number", data: `5`, wantErr: "unexpected JSON token: 5"},
			{name: "string", data: `"x"`, wantErr: "unexpected JSON token: \""},
			{name: "bad array element", data: `[{}]`, wantErr: "FieldValue"},
			{name: "bad lookup field", data: `{"id":5}`, wantErr: "id"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var u opensearchapi.CommonQueryDSLTermsQueryField
				err := u.UnmarshalJSON([]byte(tt.data))
				if tt.wantErr != "" {
					require.ErrorContains(t, err, tt.wantErr)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tt.want, u.Type())
			})
		}
	})

	t.Run("UnmarshalJSONFrom", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			data    string
			want    opensearchapi.CommonQueryDSLTermsQueryFieldType
			wantErr bool
		}{
			{name: "array", data: `["a"]`, want: opensearchapi.CommonQueryDSLTermsQueryFieldArrayType},
			{name: "lookup", data: `{"index":"i","id":"1","path":"p"}`, want: opensearchapi.CommonQueryDSLTermsQueryFieldLookupType},
			{name: "null", data: `null`, want: opensearchapi.CommonQueryDSLTermsQueryFieldUnknownType},
			{name: "number", data: `5`, wantErr: true},
			{name: "bad array element", data: `[{}]`, wantErr: true},
			{name: "bad lookup field", data: `{"id":5}`, wantErr: true},
			{name: "truncated literal", data: `tru`, wantErr: true},
			{name: "truncated null", data: `nul`, wantErr: true},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				var u opensearchapi.CommonQueryDSLTermsQueryField
				// jsonx.Unmarshal decodes from bytes and so reaches UnmarshalJSONFrom.
				err := jsonx.Unmarshal([]byte(tt.data), &u)
				if tt.wantErr {
					require.Error(t, err)
					return
				}
				require.NoError(t, err)
				require.Equal(t, tt.want, u.Type())
				require.JSONEq(t, tt.data, string(u.RawJSON()))
			})
		}
	})

	t.Run("UnmarshalJSONFrom on a truncated value", func(t *testing.T) {
		t.Parallel()

		for _, data := range []string{`tru`, `nul`, `[`, `{"id"`} {
			var u opensearchapi.CommonQueryDSLTermsQueryField
			require.Error(t, u.UnmarshalJSONFrom(jsonx.NewTextDecoder(strings.NewReader(data))), data)
		}
	})

	t.Run("marshal", func(t *testing.T) {
		t.Parallel()

		var raw opensearchapi.CommonQueryDSLTermsQueryField
		raw.SetRaw(json.RawMessage(`["raw"]`))
		tests := []struct {
			name  string
			value opensearchapi.CommonQueryDSLTermsQueryField
			want  string
		}{
			{
				name: "constructed",
				value: opensearchapi.NewCommonQueryDSLTermsQueryFieldFromArray(
					[]opensearchapi.FieldValue{opensearchapi.NewFieldValueFromString("a")}),
				want: `["a"]`,
			},
			{name: "raw only", value: raw, want: `["raw"]`},
			{name: "zero", want: `null`},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				got, err := tt.value.MarshalJSON()
				require.NoError(t, err)
				require.JSONEq(t, tt.want, string(got))

				// jsonx.Marshal reaches MarshalJSONTo.
				got, err = jsonx.Marshal(tt.value)
				require.NoError(t, err)
				require.JSONEq(t, tt.want, string(got))
			})
		}
	})
}
