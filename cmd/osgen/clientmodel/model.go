// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package clientmodel reads the resolved client model (format 0.2.0) that
// opensearch-api-specification writes into the bundled spec (`npm run merge`)
// as `x-model` extensions: every schema as a flat object, union or alias entry
// with merged fields, requiredness, union kind and constraints, so a generator
// reads types instead of interpreting allOf/oneOf/anyOf.
//
// [Lower] turns the model back into composition-free OpenAPI schemas, which is
// how osgen's walker takes its type input from it.
package clientmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"go.yaml.in/yaml/v3"
)

// Format and Version identify the model format this package reads.
const (
	Format  = "opensearch-client-model"
	Version = "0.2.0"
)

// ModelKey is the extension that carries the model in the spec: on
// `components` (format and version), on every component schema (its
// [TypeEntry]) and on every response and request body component (its
// [BodyEntry]).
const ModelKey = "x-model"

// Model is a parsed client model.
type Model struct {
	Format        string                `json:"format"`
	Version       string                `json:"version"`
	Types         map[string]*TypeEntry `json:"types"`
	Responses     map[string]*BodyEntry `json:"responses"`
	RequestBodies map[string]*BodyEntry `json:"request_bodies"`
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

// LoadSpec reads and checks the client model of the OpenAPI file at path, from
// its x-model extensions (see [FromSpec]).
func LoadSpec(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return FromSpec(data)
}

// FromSpec reads and checks the client model of an OpenAPI document (YAML or
// JSON) from its x-model extensions: `components.x-model` is the format and
// version, the x-model of each component schema is that type, and the x-model
// of each response and request body component is that body. Components
// without x-model are not in the model.
//
// The document is read as YAML nodes rather than through kin-openapi, whose
// extension maps lose key order: the order of fields and variants is the
// order of the generated code.
func FromSpec(data []byte) (*Model, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("client model: %w", err)
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) == 1 {
		root = root.Content[0]
	}
	components := mappingValue(root, "components")
	header := mappingValue(components, ModelKey)
	if header == nil {
		return nil, fmt.Errorf("client model: the spec has no components.%s; "+
			"use a spec merged by opensearch-api-specification (npm run merge)", ModelKey)
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	if header.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("client model: components.%s is not an object", ModelKey)
	}
	for i := 0; i+1 < len(header.Content); i += 2 {
		if key := header.Content[i].Value; key == "format" || key == "version" {
			if err := writeEntry(&buf, key, header.Content[i+1]); err != nil {
				return nil, err
			}
			buf.WriteByte(',')
		}
	}
	for i, section := range [][2]string{{"types", "schemas"}, {"responses", "responses"}, {"request_bodies", "requestBodies"}} {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := writeSection(&buf, section[0], mappingValue(components, section[1])); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return Parse(buf.Bytes())
}

// writeSection writes `"key": {name: x-model, …}` for the components of a
// components section that have x-model, in document order.
func writeSection(buf *bytes.Buffer, key string, section *yaml.Node) error {
	if err := writeKey(buf, key); err != nil {
		return err
	}
	buf.WriteByte('{')
	if section != nil && section.Kind == yaml.MappingNode {
		n := 0
		for i := 0; i+1 < len(section.Content); i += 2 {
			model := mappingValue(section.Content[i+1], ModelKey)
			if model == nil {
				continue
			}
			if n > 0 {
				buf.WriteByte(',')
			}
			n++
			if err := writeEntry(buf, section.Content[i].Value, model); err != nil {
				return fmt.Errorf("client model: %s: %w", section.Content[i].Value, err)
			}
		}
	}
	buf.WriteByte('}')
	return nil
}

// writeEntry writes `"key": value`.
func writeEntry(buf *bytes.Buffer, key string, value *yaml.Node) error {
	if err := writeKey(buf, key); err != nil {
		return err
	}
	return writeJSON(buf, value)
}

// writeKey writes `"key":`.
func writeKey(buf *bytes.Buffer, key string) error {
	kb, err := json.Marshal(key)
	if err != nil {
		return err
	}
	buf.Write(kb)
	buf.WriteByte(':')
	return nil
}

// mappingValue is the value of key in a YAML mapping, nil if n is not a
// mapping or has no such key.
func mappingValue(n *yaml.Node, key string) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// writeJSON writes a YAML node as JSON, keeping the key order of mappings.
func writeJSON(buf *bytes.Buffer, n *yaml.Node) error {
	switch n.Kind {
	case yaml.AliasNode:
		return writeJSON(buf, n.Alias)
	case yaml.MappingNode:
		buf.WriteByte('{')
		for i := 0; i+1 < len(n.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeEntry(buf, n.Content[i].Value, n.Content[i+1]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case yaml.SequenceNode:
		buf.WriteByte('[')
		for i, item := range n.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeJSON(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case yaml.ScalarNode:
		var v any = n.Value // strings, and tags JSON has no type for (e.g. timestamps)
		switch n.ShortTag() {
		case "!!null", "!!bool", "!!int", "!!float":
			if err := n.Decode(&v); err != nil {
				return err
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		buf.Write(b)
	case yaml.DocumentNode:
		return fmt.Errorf("line %d: unexpected YAML document", n.Line)
	default:
		return fmt.Errorf("line %d: unexpected YAML node kind %v", n.Line, n.Kind)
	}
	return nil
}

// Parse decodes a client model in its JSON form ({format, version, types,
// responses, request_bodies}) and checks its format and version.
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
