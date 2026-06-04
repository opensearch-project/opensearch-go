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

	"github.com/opensearch-project/opensearch-go/v4/v5preview/opensearchapi"
)

// TestUnionConstructors exercises the NewXFromY constructor pattern + the
// SetRaw escape hatch + JSON round-trip on a real two-branch lazy union
// (CommonQueryDSLQueryContainer | []CommonQueryDSLQueryContainer). It is
// regression-coverage on the codegen template's constructor emission, not
// exhaustive coverage of every union in the package.
//
// The "transform" hook lets a row apply additional mutation between
// "build" and assertion: SetRaw on a populated value, or a marshal/
// unmarshal round-trip to confirm the typed branch survives the wire.
func TestUnionConstructors(t *testing.T) {
	t.Parallel()

	type union = opensearchapi.CommonQueryDSLBoolQueryFilter
	type unionType = opensearchapi.CommonQueryDSLBoolQueryFilterType
	const (
		unknownT = opensearchapi.CommonQueryDSLBoolQueryFilterUnknownType
		objT     = opensearchapi.CommonQueryDSLBoolQueryFilterCommonQueryDSLQueryContainerType
		arrT     = opensearchapi.CommonQueryDSLBoolQueryFilterArrayType
	)

	tests := []struct {
		name         string
		build        func() union
		transform    func(*union) // optional; nil = none
		wantType     unionType
		wantBytes    string // JSONEq target; empty = just assert non-empty
		wantArrayLen int    // -1 = don't check; >=0 = check Array() length
	}{
		{
			name: "object branch via NewFromCommonQueryDSLQueryContainer",
			build: func() union {
				return opensearchapi.NewCommonQueryDSLBoolQueryFilterFromCommonQueryDSLQueryContainer(
					opensearchapi.CommonQueryDSLQueryContainer{},
				)
			},
			wantType:     objT,
			wantArrayLen: -1,
		},
		{
			name: "array branch via NewFromArray",
			build: func() union {
				return opensearchapi.NewCommonQueryDSLBoolQueryFilterFromArray(
					[]opensearchapi.CommonQueryDSLQueryContainer{{}, {}},
				)
			},
			wantType:     arrT,
			wantArrayLen: 2,
		},
		{
			// SetRaw deliberately leaves typ == Unknown; bytes are
			// emitted verbatim by MarshalJSON.
			name: "raw branch via SetRaw on zero value",
			build: func() union {
				var u union
				u.SetRaw(json.RawMessage(`{"match_all":{}}`))
				return u
			},
			wantType:     unknownT,
			wantBytes:    `{"match_all":{}}`,
			wantArrayLen: -1,
		},
		{
			// SetRaw on a populated union must clear the typed branch
			// so MarshalJSON returns raw, not the stale typed value.
			name: "SetRaw overrides previously typed branch",
			build: func() union {
				return opensearchapi.NewCommonQueryDSLBoolQueryFilterFromCommonQueryDSLQueryContainer(
					opensearchapi.CommonQueryDSLQueryContainer{},
				)
			},
			transform: func(u *union) {
				u.SetRaw(json.RawMessage(`{"match_all":{}}`))
			},
			wantType:     unknownT,
			wantBytes:    `{"match_all":{}}`,
			wantArrayLen: -1,
		},
		{
			// Marshal a typed value, unmarshal back, confirm the typed
			// branch survives the wire round-trip.
			name: "round-trip via Marshal+Unmarshal preserves array branch",
			build: func() union {
				return opensearchapi.NewCommonQueryDSLBoolQueryFilterFromArray(
					[]opensearchapi.CommonQueryDSLQueryContainer{{}, {}},
				)
			},
			transform: func(u *union) {
				wire, err := json.Marshal(u)
				if err != nil {
					panic(err)
				}
				var decoded union
				if err := json.Unmarshal(wire, &decoded); err != nil {
					panic(err)
				}
				*u = decoded
			},
			wantType:     arrT,
			wantArrayLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := tt.build()
			if tt.transform != nil {
				tt.transform(&u)
			}

			require.Equal(t, tt.wantType, u.Type())

			out, err := json.Marshal(u)
			require.NoError(t, err)
			require.NotEmpty(t, out)
			if tt.wantBytes != "" {
				require.JSONEq(t, tt.wantBytes, string(out))
			}

			if tt.wantArrayLen >= 0 {
				require.Len(t, u.Array(), tt.wantArrayLen)
			}
		})
	}
}
