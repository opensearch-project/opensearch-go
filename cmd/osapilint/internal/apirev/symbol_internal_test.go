// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEquivalentSymbol(t *testing.T) {
	t.Parallel()

	const structA = `struct{typ @.OldType; v int "json:\"v\""; w string}`
	typ := func(under string, ms ...Method) Symbol {
		return Symbol{Kind: SymbolType, Type: under, Methods: ms}
	}
	cnst := func(ty, val string) Symbol { return Symbol{Kind: SymbolConst, Type: ty, Value: val} }
	method := func(name, sig string, ptr bool) Method {
		return Method{Name: name, Signature: sig, PointerReceiver: ptr}
	}

	tests := []struct {
		name    string
		from    Symbol
		to      Symbol
		renamed map[string]string
		wantErr string
	}{
		{name: "identical type", from: typ("struct{a int}"), to: typ("struct{a int}")},
		{
			name:    "own-package names mapped",
			from:    typ(structA, method("Get", "func() @.OldType", false)),
			to:      typ(`struct{typ @.NewType; v int "json:\"v\""; w string}`, method("Get", "func() @.NewType", false)),
			renamed: map[string]string{"OldType": "NewType"},
		},
		{
			name:    "whole tokens only",
			from:    typ("struct{a @.OldTypeX}"),
			to:      typ("struct{a @.NewTypeX}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "tag naming a renamed type is not mapped",
			from:    typ(`struct{a int "json:\"OldType\""}`),
			to:      typ(`struct{a int "json:\"NewType\""}`),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "tag holding a marked name is not mapped",
			from:    typ(`struct{a int "json:\"@.OldType\""}`),
			to:      typ(`struct{a int "json:\"@.NewType\""}`),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "backquoted span is not mapped",
			from:    typ("struct{a int `json:\"@.OldType\"`}"),
			to:      typ("struct{a int `json:\"@.NewType\"`}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "field named like a renamed type is not mapped",
			from:    typ("struct{OldType int}"),
			to:      typ("struct{NewType int}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "method named like a renamed type is not mapped",
			from:    typ("int", method("OldType", "func()", false)),
			to:      typ("int", method("NewType", "func()", false)),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: `method "OldType"`,
		},
		{
			name:    "other-package type with a renamed name is not mapped",
			from:    typ("struct{a example.com/other.OldType}"),
			to:      typ("struct{a example.com/other.NewType}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "target still using the old name is a difference",
			from:    typ("struct{a @.OldType}"),
			to:      typ("struct{a @.OldType}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "target-only methods name the sorted first",
			from:    typ("int"),
			to:      typ("int", method("B", "func()", false), method("A", "func()", false)),
			wantErr: `method "A" only on target`,
		},
		{
			name:    "non-ASCII letter extends the marker name",
			from:    typ("struct{a @.OldTypeé}"),
			to:      typ("struct{a @.NewTypeé}"),
			renamed: map[string]string{"OldType": "NewType"},
			wantErr: "type",
		},
		{
			name:    "tag differing only in a major version segment",
			from:    typ(`struct{a int "x/v1.y"}`),
			to:      typ(`struct{a int "x/v2.y"}`),
			wantErr: "type",
		},
		{
			name:    "backquoted tag differing only in a major version segment",
			from:    typ("struct{a int `x/v1/y`}"),
			to:      typ("struct{a int `x/v2/y`}"),
			wantErr: "type",
		},
		{
			name: "cross-package major version",
			from: typ("struct{a example.com/m/v5/pkg.T}"),
			to:   typ("struct{a example.com/m/v6/pkg.T}"),
		},
		{name: "kind differs type vs alias", from: typ("int"), to: Symbol{Kind: SymbolAlias, Type: "int"}, wantErr: "kind"},
		{name: "kind differs const vs func", from: cnst("int", "1"), to: Symbol{Kind: SymbolFunc, Type: "int"}, wantErr: "kind"},
		{name: "const renumbered", from: cnst("T", "1"), to: cnst("T", "2"), wantErr: "value"},
		{name: "const type differs", from: cnst("T", "1"), to: cnst("U", "1"), wantErr: "type"},
		{name: "field reordered", from: typ("struct{a int; b int}"), to: typ("struct{b int; a int}"), wantErr: "type"},
		{name: "field retagged", from: typ(`struct{a int "json:\"a\""}`), to: typ(`struct{a int "json:\"b\""}`), wantErr: "type"},
		{name: "field type differs", from: typ("struct{a int}"), to: typ("struct{a int64}"), wantErr: "type"},
		{
			name:    "method missing",
			from:    typ("int", method("A", "func()", false), method("B", "func()", false)),
			to:      typ("int", method("A", "func()", false)),
			wantErr: `method "B"`,
		},
		{
			name:    "method signature differs",
			from:    typ("int", method("A", "func() int", false)),
			to:      typ("int", method("A", "func() string", false)),
			wantErr: `method "A"`,
		},
		{
			name:    "receiver kind differs",
			from:    typ("int", method("A", "func()", false)),
			to:      typ("int", method("A", "func()", true)),
			wantErr: `method "A"`,
		},
		{
			name:    "func signature differs",
			from:    Symbol{Kind: SymbolFunc, Type: "func(int)"},
			to:      Symbol{Kind: SymbolFunc, Type: "func(string)"},
			wantErr: "type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := EquivalentSymbol(tt.from, tt.to, tt.renamed)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
