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

// 1. keyword branch decodes and Type() reports it
func TestDiscKeyword(t *testing.T) {
	var p opensearchapi.CommonMappingProperty
	require.NoError(t, json.Unmarshal([]byte(`{"type":"keyword","index":true}`), &p))
	require.Equal(t, opensearchapi.CommonMappingPropertyKeywordPropertyType, p.Type())
	require.Equal(t, "KeywordProperty", p.Type().String())
	kw, err := p.KeywordProperty()
	require.NoError(t, err)
	require.NotNil(t, kw)
}

// 2. the wrong accessor returns *UnionBranchError
func TestDiscWrongAccessor(t *testing.T) {
	var p opensearchapi.CommonMappingProperty
	require.NoError(t, json.Unmarshal([]byte(`{"type":"keyword"}`), &p))
	_, err := p.BinaryProperty()
	require.Error(t, err)
	var be *opensearchapi.UnionBranchError
	require.ErrorAs(t, err, &be)
	require.Equal(t, "CommonMappingProperty", be.Union)
	require.Equal(t, "BinaryProperty", be.Want)
	require.Equal(t, "KeywordProperty", be.Got)
	t.Logf("err = %v", err)
}

// 3. absent discriminator falls back per x-default: object
func TestDiscXDefault(t *testing.T) {
	var p opensearchapi.CommonMappingProperty
	require.NoError(t, json.Unmarshal([]byte(`{"properties":{"a":{"type":"keyword"}}}`), &p))
	require.Equal(t, opensearchapi.CommonMappingPropertyObjectPropertyType, p.Type(),
		"a bare {properties:...} is an implicit object mapping (x-default)")
	t.Logf("x-default branch = %s", p.Type().String())
}

// 4. an unrecognized discriminator errors NAMING the value
func TestDiscUnknownValue(t *testing.T) {
	var p opensearchapi.CommonMappingProperty
	err := json.Unmarshal([]byte(`{"type":"not_a_real_type"}`), &p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not_a_real_type")
	require.Equal(t, opensearchapi.CommonMappingPropertyUnknownType, p.Type())
	t.Logf("err = %v", err)
}

// 4b. absent property with NO x-default is an error naming the property
func TestDiscAbsentNoDefault(t *testing.T) {
	var c opensearchapi.ClusterRemoteInfoCluster
	err := json.Unmarshal([]byte(`{"seeds":["a:9300"]}`), &c)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mode")
	t.Logf("err = %v", err)
}

// 4c. discriminator on a non-"type" property
func TestDiscModeProperty(t *testing.T) {
	var c opensearchapi.ClusterRemoteInfoCluster
	require.NoError(t, json.Unmarshal([]byte(`{"mode":"sniff","seeds":["a:9300"],"connected":true}`), &c))
	require.Equal(t, "ClusterRemoteSniffInfo", c.Type().String())
}

// 5. AsSum() on histogram bytes errors rather than returning a silent zero
func TestRequestSelectedWrongShape(t *testing.T) {
	var agg opensearchapi.CommonAggregationsAggregate
	require.NoError(t, json.Unmarshal([]byte(`{"buckets":[{"key":1,"doc_count":2}]}`), &agg))

	_, err := agg.AsSum()
	require.Error(t, err, "histogram bytes must not decode silently as Sum")
	var be *opensearchapi.UnionBranchError
	require.ErrorAs(t, err, &be)
	require.Equal(t, "Sum", be.Want)
	require.Error(t, be.Unwrap(), "must wrap the decode failure")
	t.Logf("err = %v", err)

	// the correct accessor still works
	h, err := agg.AsHistogram()
	require.NoError(t, err)
	require.NotNil(t, h)
}

// 5b. documented limitation: same-shape branches are NOT distinguishable
func TestRequestSelectedSameShapeIndistinguishable(t *testing.T) {
	var agg opensearchapi.CommonAggregationsAggregate
	require.NoError(t, json.Unmarshal([]byte(`{"value":42}`), &agg))
	s, err := agg.AsSum()
	require.NoError(t, err)
	require.NotNil(t, s.Value)
	a, err := agg.AsAvg()
	require.NoError(t, err, "avg and sum share {\"value\":N}; only the request knows")
	require.NotNil(t, a.Value)
}

// 5c. empty union is distinguishable from a decoded zero via IsZero
func TestRequestSelectedIsZero(t *testing.T) {
	var empty opensearchapi.CommonAggregationsAggregate
	require.True(t, empty.IsZero())
	v, err := empty.AsSum()
	require.NoError(t, err)
	require.Nil(t, v.Value)

	var zeroDecoded opensearchapi.CommonAggregationsAggregate
	require.NoError(t, json.Unmarshal([]byte(`{"value":0}`), &zeroDecoded))
	require.False(t, zeroDecoded.IsZero(), "a decoded zero is not an empty union")
}

// 6. round-trip: marshal a constructed discriminated union and read it back
func TestDiscRoundTrip(t *testing.T) {
	p := opensearchapi.NewCommonMappingPropertyFromKeywordProperty(
		opensearchapi.CommonMappingKeywordProperty{},
	)
	wire, err := json.Marshal(p)
	require.NoError(t, err)
	var back opensearchapi.CommonMappingProperty
	require.NoError(t, json.Unmarshal(wire, &back))
	require.Equal(t, opensearchapi.CommonMappingPropertyKeywordPropertyType, back.Type())
}

// 6b. the binary and match_only_text constructors set their type too
func TestDiscRoundTripBinaryAndMatchOnlyText(t *testing.T) {
	for _, p := range []opensearchapi.CommonMappingProperty{
		opensearchapi.NewCommonMappingPropertyFromBinaryProperty(opensearchapi.CommonMappingBinaryProperty{}),
		opensearchapi.NewCommonMappingPropertyFromMatchOnlyTextProperty(opensearchapi.CommonMappingMatchOnlyTextProperty{}),
	} {
		wire, err := json.Marshal(p)
		require.NoError(t, err)
		var back opensearchapi.CommonMappingProperty
		require.NoError(t, json.Unmarshal(wire, &back), string(wire))
		require.Equal(t, p.Type(), back.Type())
	}
}

// decodeAnalyzer decodes data as an analyzer and returns the decoded branch
// and its re-marshaled wire form.
func decodeAnalyzer(data []byte) (string, []byte, error) {
	var a opensearchapi.CommonAnalysisAnalyzer
	if err := json.Unmarshal(data, &a); err != nil {
		return "", nil, err
	}
	wire, err := json.Marshal(a)
	return a.Type().String(), wire, err
}

// decodeNormalizer decodes data as a normalizer and returns the decoded branch
// and its re-marshaled wire form.
func decodeNormalizer(data []byte) (string, []byte, error) {
	var n opensearchapi.CommonAnalysisNormalizer
	if err := json.Unmarshal(data, &n); err != nil {
		return "", nil, err
	}
	wire, err := json.Marshal(n)
	return n.Type().String(), wire, err
}

// 7. OpenSearch treats an analyzer or normalizer with no type, or a null one,
// as custom, and returns stored settings without one, so the absent type
// decodes as the custom branch and marshals back as an explicit "custom" rather
// than an empty type.
func TestDiscAnalysisCustomDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		decode   func(data []byte) (branch string, wire []byte, err error)
		payload  string
		wantType string
		wantWire string
	}{
		{
			name:     "analyzer without type",
			decode:   decodeAnalyzer,
			payload:  `{"filter":["lowercase"],"tokenizer":"keyword"}`,
			wantType: "CustomAnalyzer",
			wantWire: `{"filter":["lowercase"],"tokenizer":"keyword","type":"custom"}`,
		},
		{
			name:     "analyzer with explicit custom type",
			decode:   decodeAnalyzer,
			payload:  `{"type":"custom","filter":["lowercase"],"tokenizer":"keyword"}`,
			wantType: "CustomAnalyzer",
			wantWire: `{"filter":["lowercase"],"tokenizer":"keyword","type":"custom"}`,
		},
		{
			name:     "analyzer with null type",
			decode:   decodeAnalyzer,
			payload:  `{"type":null,"filter":["lowercase"],"tokenizer":"keyword"}`,
			wantType: "CustomAnalyzer",
			wantWire: `{"filter":["lowercase"],"tokenizer":"keyword","type":"custom"}`,
		},
		{
			name:     "normalizer without type",
			decode:   decodeNormalizer,
			payload:  `{"filter":["lowercase"]}`,
			wantType: "CustomNormalizer",
			wantWire: `{"filter":["lowercase"],"type":"custom"}`,
		},
		{
			name:     "normalizer with null type",
			decode:   decodeNormalizer,
			payload:  `{"type":null,"filter":["lowercase"]}`,
			wantType: "CustomNormalizer",
			wantWire: `{"filter":["lowercase"],"type":"custom"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			branch, wire, err := tt.decode([]byte(tt.payload))
			require.NoError(t, err)
			require.Equal(t, tt.wantType, branch)
			require.JSONEq(t, tt.wantWire, string(wire))
		})
	}
}

// 7a. An analyzer with no type and no tokenizer is not a valid custom analyzer,
// and the server never returns one. The decoder still takes the custom default
// without error and does not validate required custom fields; this pins that
// behavior so a change to it is deliberate.
func TestDiscAnalyzerNoTypeNoTokenizer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty object", payload: `{}`},
		{name: "filter only", payload: `{"filter":["lowercase"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			branch, _, err := decodeAnalyzer([]byte(tt.payload))
			require.NoError(t, err)
			require.Equal(t, "CustomAnalyzer", branch)
		})
	}
}

// 7b. a get-index response for an index created with a lowercase keyword
// analyzer decodes; the stored settings carry no analyzer type.
func TestDiscIndicesGetAnalyzerWithoutType(t *testing.T) {
	t.Parallel()

	body := `{"my-index":{"aliases":{},"mappings":{},"settings":{"index":{"analysis":{` +
		`"analyzer":{"lowercase_keyword":{"filter":["lowercase"],"tokenizer":"keyword"}},` +
		`"normalizer":{"lowercase_normalizer":{"filter":["lowercase"]}}}}}}}`

	var resp opensearchapi.IndicesGetResp
	require.NoError(t, json.Unmarshal([]byte(body), &resp))

	analysis := resp.Entries["my-index"].Settings.Index.Analysis
	analyzer := analysis.Analyzer["lowercase_keyword"]
	custom, err := analyzer.CustomAnalyzer()
	require.NoError(t, err)
	require.Equal(t, "keyword", custom.Tokenizer)
	require.Equal(t, []string{"lowercase"}, custom.Filter)

	normalizer := analysis.Normalizer["lowercase_normalizer"]
	_, err = normalizer.CustomNormalizer()
	require.NoError(t, err)
}
