// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersionRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		minFlag         string
		maxFlag         string
		preserveOpt     bool
		wantMinVer      string
		wantMinOp       string
		wantMaxVer      string
		wantMaxOp       string
		wantErr         bool
	}{
		{
			name:       "defaults epoch/latest",
			minFlag:    "epoch",
			maxFlag:    "latest",
			wantMinVer: "",
			wantMinOp:  ">=",
			wantMaxVer: "",
			wantMaxOp:  "<=",
		},
		{
			name:       "explicit versions no operators",
			minFlag:    "2.0.0",
			maxFlag:    "3.0.0",
			wantMinVer: "2.0.0",
			wantMinOp:  ">=",
			wantMaxVer: "3.0.0",
			wantMaxOp:  "<=",
		},
		{
			name:       "short version normalized",
			minFlag:    "2.0",
			maxFlag:    "3",
			wantMinVer: "2.0.0",
			wantMinOp:  ">=",
			wantMaxVer: "3.0.0",
			wantMaxOp:  "<=",
		},
		{
			name:       "explicit operators",
			minFlag:    ">1.0.0",
			maxFlag:    "<3.0.0",
			wantMinVer: "1.0.0",
			wantMinOp:  ">",
			wantMaxVer: "3.0.0",
			wantMaxOp:  "<",
		},
		{
			name:       "ge/le operators",
			minFlag:    ">=2.4.0",
			maxFlag:    "<=2.9.0",
			wantMinVer: "2.4.0",
			wantMinOp:  ">=",
			wantMaxVer: "2.9.0",
			wantMaxOp:  "<=",
		},
		{
			name:       "empty strings treated as unbounded",
			minFlag:    "",
			maxFlag:    "",
			wantMinVer: "",
			wantMinOp:  ">=",
			wantMaxVer: "",
			wantMaxOp:  "<=",
		},
		{
			name:        "preserve optional flag passed through",
			minFlag:     "epoch",
			maxFlag:     "latest",
			preserveOpt: true,
			wantMinVer:  "",
			wantMinOp:   ">=",
			wantMaxVer:  "",
			wantMaxOp:   "<=",
		},
		{
			name:    "invalid min version",
			minFlag: "not.a.version.at.all",
			maxFlag: "latest",
			wantErr: true,
		},
		{
			name:    "invalid max version",
			minFlag: "epoch",
			maxFlag: "also-bad",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vr, err := ParseVersionRange(tt.minFlag, tt.maxFlag, versionEpoch, tt.preserveOpt)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantMinVer, vr.Min.Version)
			require.Equal(t, tt.wantMinOp, vr.Min.Operator)
			require.Equal(t, tt.wantMaxVer, vr.Max.Version)
			require.Equal(t, tt.wantMaxOp, vr.Max.Operator)
			require.Equal(t, tt.preserveOpt, vr.PreserveOptional)
		})
	}
}

func TestVersionRange_IsAll(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		vr   VersionRange
		want bool
	}{
		{
			name: "both unbounded",
			vr:   VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Operator: "<="}},
			want: true,
		},
		{
			name: "min bounded",
			vr:   VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}},
			want: false,
		},
		{
			name: "max bounded",
			vr:   VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.vr.IsAll())
		})
	}
}

func TestVersionRange_Includes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		vr                VersionRange
		versionAdded      string
		versionRemoved    string
		versionDeprecated string
		want              bool
	}{
		{
			name:         "unbounded includes everything",
			vr:           VersionRange{},
			versionAdded: "5.0.0",
			want:         true,
		},
		{
			name:           "unbounded includes removed items",
			vr:             VersionRange{},
			versionRemoved: "2.0.0",
			want:           true,
		},
		{
			name:         "no version annotations always included",
			vr:           VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			versionAdded: "",
			want:         true,
		},
		{
			name:         "added within max included",
			vr:           VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			versionAdded: "2.4.0",
			want:         true,
		},
		{
			name:         "added exactly at max included with <=",
			vr:           VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			versionAdded: "3.0.0",
			want:         true,
		},
		{
			name:         "added exactly at max excluded with <",
			vr:           VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<"}},
			versionAdded: "3.0.0",
			want:         false,
		},
		{
			name:         "added after max excluded",
			vr:           VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			versionAdded: "3.1.0",
			want:         false,
		},
		{
			name:           "removed before min excluded",
			vr:             VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}},
			versionRemoved: "1.5.0",
			want:           false,
		},
		{
			name:           "removed exactly at min excluded with >=",
			vr:             VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}},
			versionRemoved: "2.0.0",
			want:           false,
		},
		{
			name:           "removed after min included",
			vr:             VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}},
			versionRemoved: "3.0.0",
			want:           true,
		},
		{
			name:           "removed exactly at min included with >",
			vr:             VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">"}, Max: VersionBound{Operator: "<="}},
			versionRemoved: "2.0.0",
			want:           false,
		},
		{
			name:           "short version strings handled",
			vr:             VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			versionAdded:   "2.4",
			versionRemoved: "",
			want:           true,
		},
		{
			name:           "both added and removed within window",
			vr:             VersionRange{Min: VersionBound{Version: "1.0.0", Operator: ">="}, Max: VersionBound{Version: "4.0.0", Operator: "<="}},
			versionAdded:   "2.0.0",
			versionRemoved: "3.0.0",
			want:           true,
		},
		{
			name:           "added within but removed before min",
			vr:             VersionRange{Min: VersionBound{Version: "3.0.0", Operator: ">="}, Max: VersionBound{Version: "4.0.0", Operator: "<="}},
			versionAdded:   "1.0.0",
			versionRemoved: "2.0.0",
			want:           false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.vr.Includes(tt.versionAdded, tt.versionRemoved, tt.versionDeprecated))
		})
	}
}

func TestVersionRange_Exclusion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		vr                VersionRange
		itemName          string
		versionAdded      string
		versionRemoved    string
		versionDeprecated string
		wantNil           bool
		wantIsOlder       bool
		wantReason        string
	}{
		{
			name:         "included returns nil",
			vr:           VersionRange{Min: VersionBound{Version: "1.0.0", Operator: ">="}, Max: VersionBound{Version: "3.0.0", Operator: "<="}},
			itemName:     "Foo",
			versionAdded: "2.0.0",
			wantNil:      true,
		},
		{
			name:           "removed before min is older",
			vr:             VersionRange{Min: VersionBound{Version: "3.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}},
			itemName:       "Bar",
			versionRemoved: "2.0.0",
			wantIsOlder:    true,
			wantReason:     "removed in OpenSearch 2.0.0",
		},
		{
			name:         "added after max is newer",
			vr:           VersionRange{Min: VersionBound{Operator: ">="}, Max: VersionBound{Version: "2.0.0", Operator: "<="}},
			itemName:     "Baz",
			versionAdded: "3.0.0",
			wantIsOlder:  false,
			wantReason:   "requires OpenSearch >= 3.0.0",
		},
		{
			name:              "deprecated treated as removed",
			vr:                VersionRange{Min: VersionBound{Version: "2.0.0", Operator: ">="}, Max: VersionBound{Operator: "<="}, RemoveDeprecated: VersionBound{Version: "2.0.0", Operator: "<="}},
			itemName:          "Qux",
			versionDeprecated: "1.5.0",
			wantIsOlder:       true,
			wantReason:        "deprecated in OpenSearch 1.5.0 (treated as removed)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exc := tt.vr.Exclusion(tt.itemName, tt.versionAdded, tt.versionRemoved, tt.versionDeprecated)
			if tt.wantNil {
				require.Nil(t, exc)
				return
			}
			require.NotNil(t, exc)
			require.Equal(t, tt.itemName, exc.Name)
			require.Equal(t, tt.wantIsOlder, exc.IsOlder)
			require.Equal(t, tt.wantReason, exc.Reason)
		})
	}
}

func TestBreadcrumbMode_ShouldBreadcrumb(t *testing.T) {
	t.Parallel()

	older := &ExclusionReason{Name: "X", Reason: "removed", IsOlder: true}
	newer := &ExclusionReason{Name: "Y", Reason: "requires", IsOlder: false}

	tests := []struct {
		name string
		mode BreadcrumbMode
		exc  *ExclusionReason
		want bool
	}{
		{name: "all/older", mode: BreadcrumbAll, exc: older, want: true},
		{name: "all/newer", mode: BreadcrumbAll, exc: newer, want: true},
		{name: "all/nil", mode: BreadcrumbAll, exc: nil, want: false},
		{name: "older mode/older item", mode: BreadcrumbOlder, exc: older, want: true},
		{name: "older mode/newer item", mode: BreadcrumbOlder, exc: newer, want: false},
		{name: "newer mode/older item", mode: BreadcrumbNewer, exc: older, want: false},
		{name: "newer mode/newer item", mode: BreadcrumbNewer, exc: newer, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.mode.ShouldBreadcrumb(tt.exc))
		})
	}
}

func TestParseBreadcrumbMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    BreadcrumbMode
		wantErr bool
	}{
		{name: "all", input: "all", want: BreadcrumbAll},
		{name: "older", input: "older", want: BreadcrumbOlder},
		{name: "newer", input: "newer", want: BreadcrumbNewer},
		{name: "case insensitive", input: "ALL", want: BreadcrumbAll},
		{name: "trimmed", input: "  newer  ", want: BreadcrumbNewer},
		{name: "invalid", input: "bogus", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseBreadcrumbMode(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

