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
