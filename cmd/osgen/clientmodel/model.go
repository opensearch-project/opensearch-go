// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package clientmodel reads the resolved client model that
// opensearch-api-specification builds from the bundled spec
// (`npm run model`, format 0.2.0): every schema as a flat object, union or
// alias entry with merged fields, requiredness, union kind and constraints, so
// a generator reads types instead of interpreting allOf/oneOf/anyOf.
//
// [Lower] turns the model back into composition-free OpenAPI schemas, which is
// how osgen's walker takes its type input from it.
package clientmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// Format and Version identify the model format this package reads.
const (
	Format  = "opensearch-client-model"
	Version = "0.2.0"
)

// Model is a parsed client model file.
type Model struct {
	Format        string                `json:"format"`
	Version       string                `json:"version"`
	Types         map[string]*TypeEntry `json:"types"`
	Responses     map[string]*BodyEntry `json:"responses"`
	RequestBodies map[string]*BodyEntry `json:"request_bodies"`
	Diagnostics   []Diagnostic          `json:"diagnostics"`
}

// Type kinds of a [TypeEntry].
const (
	KindObject = "object"
	KindUnion  = "union"
	KindAlias  = "alias"
)

// TypeEntry is one named type. Which fields are set depends on TypeKind.
type TypeEntry struct {
	TypeKind    string `json:"type_kind"`
	Description string `json:"description,omitempty"`

	Versions *Versions `json:"versions,omitempty"`

	// object
	Bases           []string       `json:"bases,omitempty"`
	Fields          Ordered[Field] `json:"fields"`
	AdditionalField *TypeExpr      `json:"additional_fields,omitempty"`
	Constraints     []Constraint   `json:"constraints,omitempty"`

	// union
	UnionKind      string            `json:"union_kind,omitempty"`
	Exclusive      bool              `json:"exclusive,omitempty"`
	Discriminator  string            `json:"discriminator,omitempty"`
	DefaultVariant string            `json:"default_variant,omitempty"`
	Variants       Ordered[TypeExpr] `json:"variants"`
	Members        []TypeExpr        `json:"members,omitempty"`
	Titles         []*string         `json:"titles,omitempty"`
	BranchVersions []*Versions       `json:"branch_versions,omitempty"`

	// alias
	Type             *TypeExpr `json:"type,omitempty"`
	GenericParameter bool      `json:"generic_parameter,omitempty"`
}

// Field is a field of an object.
type Field struct {
	Type        TypeExpr  `json:"type"`
	Required    bool      `json:"required"`
	Description string    `json:"description,omitempty"`
	Versions    *Versions `json:"versions,omitempty"`
	// DeclaredIn is the base (a named allOf member) the field is inherited
	// from; empty for the type's own fields.
	DeclaredIn string `json:"declared_in,omitempty"`
}

// Versions is version and deprecation metadata (x-version-added,
// x-version-removed, x-version-deprecated, x-deprecation-message).
type Versions struct {
	Added              string `json:"added,omitempty"`
	Removed            string `json:"removed,omitempty"`
	Deprecated         string `json:"deprecated,omitempty"`
	DeprecationMessage string `json:"deprecation_message,omitempty"`
}

// ObjectShape is an inline object.
type ObjectShape struct {
	Bases           []string       `json:"bases,omitempty"`
	Fields          Ordered[Field] `json:"fields"`
	AdditionalField *TypeExpr      `json:"additional_fields,omitempty"`
	Constraints     []Constraint   `json:"constraints,omitempty"`
}

// TypeExpr is the type of a field, map value, array item or union member.
// Exactly one of Ref, Scalar, Enum, Array, Map, Union, Object or Any is set.
type TypeExpr struct {
	Ref       string     `json:"ref,omitempty"`
	Scalar    string     `json:"scalar,omitempty"`
	Format    string     `json:"format,omitempty"`
	Enum      []any      `json:"enum,omitempty"`
	Array     *TypeExpr  `json:"array,omitempty"`
	Map       *TypeExpr  `json:"map,omitempty"`
	Union     []TypeExpr `json:"union,omitempty"`
	UnionKind string     `json:"union_kind,omitempty"`
	Exclusive bool       `json:"exclusive,omitempty"`
	Titles    []*string  `json:"titles,omitempty"`
	// BranchVersions is each union member's version metadata, by position.
	BranchVersions []*Versions  `json:"branch_versions,omitempty"`
	Object         *ObjectShape `json:"object,omitempty"`
	Description    string       `json:"description,omitempty"`
	Any            bool         `json:"any,omitempty"`
}

// Constraint restricts which fields of an object may be set together. Which
// fields are set depends on UnionKind: `presence` (OneOf/AnyOf key sets),
// `container` (Variants), `not_all` (Keys), or another union kind (Matches and
// Types).
type Constraint struct {
	UnionKind string                    `json:"union_kind"`
	OneOf     [][]string                `json:"one_of,omitempty"`
	AnyOf     [][]string                `json:"any_of,omitempty"`
	Variants  Ordered[ContainerVariant] `json:"variants"`
	Keys      []string                  `json:"keys,omitempty"`
	Matches   string                    `json:"matches,omitempty"`
	Types     []TypeExpr                `json:"types,omitempty"`
}

// ContainerVariant is one variant of a container constraint.
type ContainerVariant struct {
	Value            TypeExpr       `json:"value"`
	ExtraFields      Ordered[Field] `json:"extra_fields"`
	ExtraConstraints []Constraint   `json:"extra_constraints"`
}

// BodyEntry is the body of a response or request body component.
type BodyEntry struct {
	MediaType   string   `json:"media_type"`
	Type        TypeExpr `json:"type"`
	Description string   `json:"description,omitempty"`
}

// Diagnostic is something the model builder could not resolve.
type Diagnostic struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Load reads and checks a client model file.
func Load(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a client model and checks its format and version.
func Parse(data []byte) (*Model, error) {
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("client model: %w", err)
	}
	if m.Format != Format {
		return nil, fmt.Errorf("client model: format %q, want %q", m.Format, Format)
	}
	if m.Version != Version {
		return nil, fmt.Errorf("client model: version %q, want %q", m.Version, Version)
	}
	return &m, nil
}

// Ordered is a JSON object decoded with its key order kept: variant order is
// branch order, which generated code depends on.
type Ordered[T any] struct {
	Keys   []string
	Values map[string]T
}

// Len is the number of entries.
func (o Ordered[T]) Len() int { return len(o.Keys) }

// Get returns the entry for key.
func (o Ordered[T]) Get(key string) (T, bool) {
	v, ok := o.Values[key]
	return v, ok
}

// UnmarshalJSON decodes a JSON object, keeping its key order.
func (o *Ordered[T]) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil { // null
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return fmt.Errorf("client model: expected an object, got %v", tok)
	}
	o.Keys, o.Values = nil, make(map[string]T)
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		var v T
		if err := dec.Decode(&v); err != nil {
			return fmt.Errorf("client model: %q: %w", key, err)
		}
		o.Keys = append(o.Keys, key)
		o.Values[key] = v
	}
	_, err = dec.Token() // '}'
	return err
}

// MarshalJSON encodes the entries in key order.
func (o Ordered[T]) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.Keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		vb, err := json.Marshal(o.Values[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
