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

	// Routing (computed during parse from group name).
	Package    string
	ImportPath string
	IsPlugin   bool
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
	Required          bool
	Deprecated        bool
	VersionAdded      string
	VersionDeprecated string
	DeprecationMsg    string
}

// ParamKind classifies query parameter serialization behavior.
type ParamKind int

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
}

// TypeKind discriminates between struct and union type forms.
type TypeKind int

const (
	TypeStruct    TypeKind = iota // plain struct with fields
	TypeUnion                    // byte-prefix discriminated union (token class dispatch)
	TypeLazyUnion                // lazy-decode union (stores raw JSON, decodes on accessor)
)

// TypeScope determines where a type is emitted.
type TypeScope int

const (
	ScopeLocal    TypeScope = iota // emitted in the operation's file
	ScopeShared                   // emitted in types_gen.go (shared across operations)
	ScopeResponse                 // top-level Resp struct (emitted in operation's file)
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
	StructName string
	Fields     []PathBuilderField
	Ops        []PathOp
}

// PathBuilderField represents a field in the generated path builder struct.
type PathBuilderField struct {
	Name     string
	Param    string
	Required bool
	IsList   bool
}

// PathOp is one instruction in the generated Build() method body.
type PathOp struct {
	Kind  PathOpKind
	Value string
}

// PathOpKind classifies path-building operations.
type PathOpKind uint8

const (
	PathOpLit        PathOpKind = iota // literal path segment
	PathOpField                       // single-value field interpolation
	PathOpList                        // comma-joined list interpolation
	PathOpIfList                      // if-branch for list field
	PathOpIfStr                       // if-branch for string field
	PathOpElseIfList                  // else-if for list field
	PathOpElseIfStr                   // else-if for string field
	PathOpElse                        // else branch
	PathOpEnd                        // end of conditional
)

// DispatchRoute describes how an operation maps to a client method.
type DispatchRoute struct {
	ReceiverType string
	MethodName   string
	FieldPath    string // dot-separated field path from Client (e.g. "Cluster", "Indices.Alias")
	TopLevel     bool
	Deprecated   bool
}
