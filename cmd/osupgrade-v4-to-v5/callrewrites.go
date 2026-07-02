package main

// callrewrites.go holds the v4 -> v5 CALL-SITE rules: method regrouping onto
// sub-clients, removed-helper replacements, and value->pointer argument
// adjustments. These are distinct from the surface-derived struct delta: they
// concern how code CALLS the client, not the shape of the request/response
// structs, so they are expressed as explicit rules verified by the osv4mig
// corpus build rather than derived from the type surfaces.

// methodRegroup rewrites a v4 client call path to its v5 sub-client path.
// Match is on the resolved method selector; the engine confirms the receiver is
// an opensearchapi client type before rewriting.
type methodRegroup struct {
	V4Path  []string // e.g. ["Indices","Count"] matched as client.Indices.Count
	V5Path  []string // e.g. ["Count"]           -> client.Count
	PtrArg  bool     // v5 method takes *Req: wrap the sole request argument in &
}

// methodRegroups covers the sub-client moves the osv4 wrapper hits. Sourced from
// opensearchapi/UPGRADING_V4_TO_V5.md's method-grouping tables plus the v5
// generated client shape (Count/DeleteByQuery moved to top-level; document ops
// to Doc; etc.). Extend as new consumers surface additional paths.
var methodRegroups = []methodRegroup{
	{V4Path: []string{"Indices", "Count"}, V5Path: []string{"Count"}, PtrArg: true},
	{V4Path: []string{"Document", "DeleteByQuery"}, V5Path: []string{"DeleteByQuery"}, PtrArg: true},
	{V4Path: []string{"Indices", "Delete"}, V5Path: []string{"Indices", "Delete"}, PtrArg: true},
	{V4Path: []string{"Indices", "Exists"}, V5Path: []string{"Indices", "Exists"}, PtrArg: true},
	{V4Path: []string{"Index"}, V5Path: []string{"Doc", "Index"}, PtrArg: false},
	// Top-level methods that stayed top-level but now take *Req in v5. Same path,
	// PtrArg only — the regroup machinery doubles as a "wrap the arg" rule.
	{V4Path: []string{"UpdateByQuery"}, V5Path: []string{"UpdateByQuery"}, PtrArg: true},
}

// helperReplacement handles opensearchapi package-level helpers removed in v5.
//   - ToPointer(x): the identity-ish helper is gone; v5 methods take *Req, so a
//     call ToPointer(x) becomes &x.
//   - NewFromClient(c): removed; flagged MANUAL because the v5 replacement
//     (constructing opensearchapi.Client from a transport client) is
//     consumer-specific and cannot be mechanically synthesized.
var removedHelpers = map[string]string{
	"ToPointer":     "addressOf", // special-cased in the engine: wrap arg in &
	"NewFromClient": "manual",    // report only
}
