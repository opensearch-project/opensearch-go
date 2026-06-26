// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ir

// Spec is the root of the intermediate representation produced by parsing the
// OpenAPI spec. It contains every operation and every resolved type.
type Spec struct {
	Operations []*Operation
	Types      []*Type
	Registry   *TypeRegistry

	// Exclusions records items dropped by the version-range filter so the
	// emit phase can render breadcrumb comments. Each entry carries the
	// item's qualified name (e.g. "search.SourceIncludes" for a param) and
	// the category it falls under. The IsOlder field on each entry says
	// whether the item was excluded by min-version (older) or max-version
	// (newer); the BreadcrumbMode filter consults this at render time.
	Exclusions Exclusions
}

// Exclusions groups version-filter casualties by category. Categories match
// the --version-breadcrumb-{operations,fields,params} CLI flags.
type Exclusions struct {
	Operations []Exclusion
	Fields     []Exclusion
	Params     []Exclusion
}

// Exclusion is a single item dropped by the version-range filter.
type Exclusion struct {
	// Name is the dotted, fully-qualified identifier for the item:
	//   - operations: "<group>" (e.g. "bulk_stream")
	//   - fields:     "<TypeName>.<FieldName>" with optional
	//                 "(req)" / "(resp)" suffix to disambiguate when the
	//                 same type name appears on both sides.
	//   - params:     "<group>.<paramName>" (e.g. "search.docvalue_fields")
	Name string
	// Reason is a short human-readable phrase like
	// "removed in OpenSearch 3.0" or "requires OpenSearch >= 2.7".
	Reason string
	// IsOlder is true when the item was excluded by min-version (it is too
	// old for the requested range), false when excluded by max-version
	// (too new). Drives the BreadcrumbOlder/BreadcrumbNewer filter modes.
	IsOlder bool
}

// Operation represents one x-operation-group with all its path variants merged.
type Operation struct {
	Group             string
	TypePrefix        string
	Description       string
	VersionAdded      string
	VersionDeprecated string
	Deprecated        bool
	DeprecationMsg    string
	DocsURL           string
	ExcludedDistros   []string

	// HTTPMethods lists all valid HTTP methods for this operation. The first
	// element is always the primary method (from the primary/shortest path
	// variant in the spec); remaining methods are sorted alphabetically.
	// For [GET, POST] operations with a body, POST is used when Body != nil.
	HTTPMethods []string

	PrimaryPath string
	HasBody     bool

	HasTypedBody    bool    // true when Body is a typed struct (not io.Reader)
	IsNDJSON        bool    // true when the request body is application/x-ndjson (e.g. _bulk, _msearch)
	RequestBody     *Type   // typed body struct (nil = io.Reader fallback)
	ReqBodySiblings []*Type // request-body-specific sibling types

	PathFields  []PathField
	QueryParams []QueryParam
	PathBuilder PathBuilder

	ResponseRef  string
	Response     *Type
	SiblingTypes []*Type

	// RespShape classifies the overall response body structure. Determines
	// how the Resp struct is rendered (plain fields vs custom unmarshal).
	RespShape    RespShape
	RespElemType *Type // element type for Map/Array shapes (e.g. the T in map[string]T or []T)

	DispatchRoutes []DispatchRoute
	IsPointerReq   bool
	IsNoBody       bool

	// ErrorWrappers lists the partial-failure wrapper-schema names this
	// operation may surface alongside its primary success response.
	// Mirrors the proposed `x-error-responses` OpenAPI extension; until
	// that extension lands upstream, cmd/osgen carries a hardcoded map
	// (see internal/errwrap.OperationWrappers) populated during
	// extraction. Order is stable (sorted) so codegen output is
	// deterministic.
	ErrorWrappers []string

	// Routing (computed during parse from group name).
	Package    string
	ImportPath string
	IsPlugin   bool

	// MethodName is the flat client method name for plugin operations,
	// computed in package main so it shares the same acronym and
	// idiomatic-abbreviation rules as core method names. Empty for core
	// operations, which carry their method names on DispatchRoutes.
	MethodName string
}

// PathField is one URL path placeholder exposed as a struct field on the Req.
type PathField struct {
	GoName   string
	WireName string
	IsList   bool
	Required bool
}

// QueryParam is one query parameter exposed on the Params struct.
type QueryParam struct {
	GoName            string
	WireName          string
	Description       string
	Default           string
	GoType            string
	Kind              ParamKind
	Group             ParamGroup
	Required          bool
	Deprecated        bool
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
}

// ParamGroup classifies a query parameter into a shared embedding group.
type ParamGroup int

// ParamGroup values: which shared params struct a parameter belongs to.
const (
	ParamGroupOperation ParamGroup = iota
	ParamGroupTimeout
	ParamGroupDebug
)

func (g ParamGroup) String() string {
	switch g {
	case ParamGroupOperation:
		return "operation"
	case ParamGroupTimeout:
		return "timeout"
	case ParamGroupDebug:
		return "debug"
	default:
		return "operation"
	}
}

// ParamKind classifies query parameter serialization behavior.
type ParamKind int

// ParamKind values: how a query parameter is serialized on the wire.
const (
	ParamString   ParamKind = iota // default string parameter
	ParamBool                      // serialized as "true"/"false"
	ParamInt                       // serialized via strconv.Itoa
	ParamDuration                  // serialized as OpenSearch duration
	ParamList                      // serialized as comma-joined strings
)

// RespShape classifies the top-level structure of a response body. Normal
// structs with named fields use RespShapeStruct. The other shapes require
// custom UnmarshalJSON/MarshalJSON to correctly capture the response data.
type RespShape int

// RespShape values: the top-level shape of a response body.
const (
	RespShapeStruct RespShape = iota // struct with explicit fields (default)
	RespShapeMap                     // map[string]T (additionalProperties with no named properties)
	RespShapeArray                   // []T (type: array with items)
	RespShapeRaw                     // json.RawMessage (empty or untyped schema)
)

// Type represents a generated Go type (struct, union, or lazy union).
type Type struct {
	Name       string
	SchemaRef  string
	Comment    string
	Kind       TypeKind
	Scope      TypeScope
	Fields     []Field
	Branches   []UnionBranch
	OwnerGroup string
	Package    string
	ImportPath string

	// Merge, when non-nil, directs a try-each union to be decoded in a
	// single json.Unmarshal pass instead of attempting each branch in turn.
	// It applies to "success | error(s)" response unions whose branches are
	// all objects and where exactly one branch is permissive (the success
	// branch, embedded as the primary) while the others carry discriminating
	// keys. See [UnionMerge].
	Merge *UnionMerge

	// LazyAccessors marks a union whose branches cannot be discriminated from
	// the wire bytes (e.g. aggregation results: avg/sum/min/max all serialize
	// as {"value": N}). Such unions are decoded lazily: UnmarshalJSON only
	// retains the raw bytes, and generated As<Branch>() accessors decode into
	// the concrete type the caller requested, on demand.
	LazyAccessors bool

	// EnumMembers holds the members of a TypeEnum (int-backed iota enum): each
	// pairs the Go const identifier with its wire value. Empty for all other
	// kinds.
	EnumMembers []EnumMember
}

// EnumMember is one member of an int-backed iota enum: a generated const of the
// enum's named int type bound to its wire value.
type EnumMember struct {
	ConstName string // Go const identifier, e.g. "RestStatusNotFound"
	Value     string // wire value, e.g. "NOT_FOUND"
}

// UnionMerge describes how to decode a "success | error(s)" union in one pass.
// The generated UnmarshalJSON decodes into a struct embedding PrimaryGoType
// plus one presence probe per [UnionMerge.Probes] entry, then fans in: if a
// discriminated branch's probe keys are all present, it decodes that branch
// (rare error path); otherwise it yields the embedded primary value (the
// common path, with no field copy).
type UnionMerge struct {
	// PrimaryGoType is the permissive branch embedded in the merged struct
	// (e.g. "GetResult"). PrimaryConst/PrimaryName carry its const and
	// accessor names for the fan-in default.
	PrimaryGoType string
	PrimaryConst  string
	PrimaryName   string

	// Probes are the presence-detection fields added alongside the embedded
	// primary, one per distinguishing JSON key across all discriminated
	// branches (deduplicated). Each is emitted as
	// `GoName json.RawMessage `json:"JSONKey"``.
	Probes []MergeProbe

	// Branches are the discriminated (error) branches in fan-in priority
	// order (newest/most-specific first).
	Branches []MergeBranch
}

// MergeProbe is a presence-detection field in a merged-union decode struct.
type MergeProbe struct {
	GoName  string // exported field name, e.g. "Disc0"
	JSONKey string // wire key probed for presence, e.g. "error"
}

// MergeBranch is one discriminated branch of a merged union. The branch is
// selected when every probe in PresentProbes decoded a non-empty value.
type MergeBranch struct {
	GoType        string   // branch type to decode into, e.g. "MGetMultiGetError"
	Const         string   // discriminant const name
	Name          string   // accessor name
	PresentProbes []string // GoNames of probes that must all be present
}

// TypeKind discriminates between struct and union type forms.
type TypeKind int

// TypeKind values: shape category for a generated Go type.
const (
	TypeStruct    TypeKind = iota // plain struct with fields
	TypeUnion                     // byte-prefix discriminated union (token class dispatch)
	TypeLazyUnion                 // lazy-decode union (stores raw JSON, decodes on accessor)
	TypeEnum                      // int-backed iota enum (named int type + const block)
)

// TypeScope determines where a type is emitted.
type TypeScope int

// TypeScope values: which file a type is emitted into.
const (
	ScopeLocal    TypeScope = iota // emitted in the operation's file
	ScopeShared                    // emitted in types_gen.go (shared across operations)
	ScopeResponse                  // top-level Resp struct (emitted in operation's file)
)

// Field represents a struct field in a generated Go type.
type Field struct {
	GoName            string
	JSONName          string
	GoType            string // unqualified type expression (e.g. "ShardStatistics", "[]string")
	IsPointer         bool
	IsEmbed           bool
	OmitEmpty         bool
	Comment           string
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
}

// UnionBranch represents one branch of a discriminated union.
type UnionBranch struct {
	Name         string
	GoType       string
	TokenClass   TokenClass
	Required     []string // required object fields for try-each discrimination
	IsRef        bool
	VersionAdded string // x-version-added (for try-each ordering, newest first)
}

// TokenClass identifies the JSON token that triggers a union branch.
type TokenClass int

// TokenClass values: JSON tokens used to discriminate union branches.
const (
	TokenObject TokenClass = iota
	TokenArray
	TokenString
	TokenNumber
	TokenBool
)

// PathBuilder holds the analyzed path-building data (trie operations) for one
// operation, used for both code generation and test generation.
type PathBuilder struct {
	StructName     string
	Fields         []PathBuilderField
	Ops            []PathOp
	PositionalDeps []PathPositionalDep
}

// PathBuilderField represents a field in the generated path builder struct.
type PathBuilderField struct {
	Name     string
	Param    string
	Required bool
	IsList   bool
}

// PathPositionalDep records that the Dependent field may only be set when
// the Predecessor field is also set. The path Build() method emits a runtime
// guard for each entry; test generators can use this list to populate
// predecessor fields whenever they populate a dependent.
type PathPositionalDep struct {
	Dependent   PathBuilderField
	Predecessor PathBuilderField
}

// PathOp is one instruction in the generated Build() method body.
type PathOp struct {
	Kind       PathOpKind
	Value      string
	Conditions []PathCaseCondition // populated for PathOpCase and PathOpIf
}

// PathCaseCondition is one field-presence test inside a switch case or
// single if{} block. Multiple conditions on one op are ANDed together.
type PathCaseCondition struct {
	Field  string
	IsList bool
}

// PathOpKind classifies path-building operations.
type PathOpKind uint8

// PathOpKind values: instruction kinds emitted into the path Build() method body.
const (
	PathOpLit          PathOpKind = iota // literal path segment
	PathOpField                          // single-value field interpolation
	PathOpList                           // comma-joined list interpolation
	PathOpSwitch                         // open switch{} block
	PathOpCase                           // case <conditions>: label
	PathOpDefault                        // default: label
	PathOpSwitchEnd                      // close switch{} block
	PathOpIf                             // open if{} block
	PathOpIfEnd                          // close if{} block
	PathOpExplainCheck                   // emit "if any-optional-set { return explain<T>(p) }"
)

// DispatchRoute describes how an operation maps to a client method.
type DispatchRoute struct {
	ReceiverType string
	MethodName   string
	FieldPath    string // dot-separated field path from Client (e.g. "Cluster", "Indices.Alias")
	TopLevel     bool
	Deprecated   bool
	// Forward, when non-empty, makes this a thin compatibility forwarder whose
	// body is `return c.<Forward>(ctx, req)` rather than a full dispatch. The
	// expression is relative to the receiver (e.g. "Doc.Bulk" on Client, or
	// "GetAll" on a sub-client).
	Forward string
}
