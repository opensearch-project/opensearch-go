// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import "maps"

// optionKind classifies where a v2 functional option's value lands in the v3
// Req struct — or that it cannot be placed mechanically (destMarker/destDropped).
type optionKind int

const (
	// destContext: the option supplies the leading ctx argument (WithContext).
	destContext optionKind = iota
	// destParams: the option value sets Req.Params.<Field>.
	destParams
	// destReqField: the option value sets Req.<Field> directly.
	destReqField
	// destDropped: the option's v3 field was removed (e.g. FilterPath) — marker.
	destDropped
	// destMarker: a semantic shape change (Header/OpaqueID) — marker this increment.
	destMarker
)

// optionDest maps one v2 WithX option to its v3 destination.
type optionDest struct {
	Kind  optionKind
	Field string // v3 field name when Kind is destParams or destReqField
	IsPtr bool   // true when the v3 destParams field is a pointer (value must be wrapped in opensearchapi.ToPointer)
}

// positionalDest maps a v2 positional arg (by index) to its v3 Req field.
type positionalDest struct {
	ReqField string // e.g. "Indices"
}

// opArgDetail carries the positional→field and option→destination detail for one
// seed op, keyed by the v2 option method name (e.g. "WithContext").
type opArgDetail struct {
	Positionals []positionalDest
	Options     map[string]optionDest
}

// universalOptions are the 7 options present on every v2 request. FilterPath is
// dropped in v3 (not in PingParams/IndicesExistsParams); Header/OpaqueID are a
// shape change to Req.Header, deferred to a later increment.
//
//nolint:gochecknoglobals // immutable data table
var universalOptions = map[string]optionDest{
	"WithContext":    {Kind: destContext},
	"WithPretty":     {Kind: destParams, Field: "Pretty"},
	"WithHuman":      {Kind: destParams, Field: "Human"},
	"WithErrorTrace": {Kind: destParams, Field: "ErrorTrace"},
	"WithFilterPath": {Kind: destDropped},
	"WithHeader":     {Kind: destMarker},
	"WithOpaqueID":   {Kind: destMarker},
}

// argDetailV2toV3 is the seed-op arg-detail table, keyed by dotted v2 path.
// Seed ops only (Ping, Indices.Exists); widen in later increments.
//
//nolint:gochecknoglobals,goconst // immutable data table; naming each repeated API name as a constant would obscure the table
var argDetailV2toV3 = map[string]opArgDetail{
	"Ping": {
		Options: universalOptions,
	},
	"Indices.Exists": {
		Positionals: []positionalDest{{ReqField: "Indices"}},
		Options: mergeOptions(universalOptions, map[string]optionDest{
			"WithAllowNoIndices":    {Kind: destParams, Field: "AllowNoIndices", IsPtr: true},
			"WithExpandWildcards":   {Kind: destParams, Field: "ExpandWildcards"},
			"WithFlatSettings":      {Kind: destParams, Field: "FlatSettings", IsPtr: true},
			"WithIgnoreUnavailable": {Kind: destParams, Field: "IgnoreUnavailable", IsPtr: true},
			"WithIncludeDefaults":   {Kind: destParams, Field: "IncludeDefaults", IsPtr: true},
			"WithLocal":             {Kind: destParams, Field: "Local", IsPtr: true},
		}),
	},
}

// mergeOptions returns a new map combining base and extra (extra wins on key
// collision). Keeps the table declarations free of repeated universal options.
func mergeOptions(base, extra map[string]optionDest) map[string]optionDest {
	out := make(map[string]optionDest, len(base)+len(extra))
	maps.Copy(out, base)
	maps.Copy(out, extra)
	return out
}
