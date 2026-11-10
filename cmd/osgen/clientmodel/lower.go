// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package clientmodel

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/getkin/kin-openapi/openapi3"
)

const schemaRefPrefix = "#/components/schemas/"

// Extensions of a component schema that are not about composition and are kept
// from the OpenAPI file: type and enum naming, and error wrapper metadata.
var keptExtensions = []string{
	"x-type-name", "x-enum-name", "x-error-status", "x-error-root-cause-type", "x-ignorable",
	"x-distributions-excluded",
}

// Lower replaces the component schemas and the JSON bodies of the response and
// request body components of spec with schemas built from the model, so that
// every type is read from the model and none needs composition interpretation:
//
//   - an object is its own fields, plus one allOf `$ref` per base (which osgen
//     embeds) when it has bases, plus one union member for a container or other
//     union constraint (whose branches osgen merges as optional fields);
//   - a union is a plain oneOf/anyOf of its members, with their titles,
//     version metadata and discriminator;
//   - version and deprecation metadata are the x-version-* extensions osgen
//     reads, on types, fields (next to the field's schema) and branches.
//
// Operations, paths and parameters keep coming from spec. Component schemas the
// model doesn't list are left as they are.
func Lower(spec *openapi3.T, m *Model) error {
	if spec.Components == nil {
		return fmt.Errorf("client model: spec has no components")
	}
	l := &lowerer{model: m, out: make(map[string]*openapi3.Schema, len(m.Types))}
	for name := range m.Types {
		l.out[name] = &openapi3.Schema{}
	}
	original := spec.Components.Schemas
	// A component that is a bare $ref (`EpochTimeUnitMillis: {$ref: UnitMillis}`)
	// resolves to its target, as kin-openapi resolves it: the same schema.
	bare := make(map[string]bool)
	described := make(map[string]string)
	for name, entry := range m.Types {
		if target, ok := bareRefTarget(name, entry, original); ok {
			bare[name] = true
			for i := 0; i < 16; i++ { // follow a chain of bare $refs
				next, ok := bareRefTarget(target, m.Types[target], original)
				if !ok {
					break
				}
				target = next
			}
			if entry.Description != "" && entry.Description != m.Types[target].Description {
				// `{$ref: X, description: …}`: X with its own description, as
				// kin-openapi resolves it; filled in once X is lowered.
				described[name] = target
				continue
			}
			l.out[name] = l.out[target]
		}
	}

	names := make([]string, 0, len(m.Types))
	for name := range m.Types {
		names = append(names, name)
	}
	sort.Strings(names)

	lowered := make(openapi3.Schemas, len(original))
	for name, ref := range original {
		lowered[name] = ref // not in the model: kept
	}
	for _, name := range names {
		entry := m.Types[name]
		if bare[name] {
			orig := original[name]
			lowered[name] = &openapi3.SchemaRef{Ref: orig.Ref, Extensions: orig.Extensions, Value: l.out[name]}
			continue
		}
		schema := l.out[name]
		*schema = *l.entry(entry)
		var orig *openapi3.SchemaRef
		if r, ok := original[name]; ok {
			orig = r
		}
		if orig != nil && orig.Value != nil {
			keepExtensions(schema, orig.Value)
			keepPropertyExtensions(schema, orig.Value)
			keepBranchDescriptions(schema, orig.Value)
			if schema.Title == "" {
				schema.Title = orig.Value.Title
			}
		}
		lowered[name] = &openapi3.SchemaRef{Value: schema}
	}
	spec.Components.Schemas = lowered
	for name, target := range described {
		*l.out[name] = *copySchema(l.out[target])
		l.out[name].Description = m.Types[name].Description
	}
	defer func() {
		for _, o := range l.overlays {
			o.apply()
		}
	}()

	for name, body := range m.Responses {
		r, ok := spec.Components.Responses[name]
		if !ok || r.Value == nil {
			continue
		}
		if mt := r.Value.Content.Get(body.MediaType); mt != nil && mt.Schema != nil {
			mt.Schema = l.body(body)
		}
	}
	for name, body := range m.RequestBodies {
		r, ok := spec.Components.RequestBodies[name]
		if !ok || r.Value == nil {
			continue
		}
		if mt := r.Value.Content.Get(body.MediaType); mt != nil && mt.Schema != nil {
			mt.Schema = l.body(body)
		}
	}
	return nil
}

type lowerer struct {
	model *Model
	out   map[string]*openapi3.Schema
	// overlays are descriptions and titles written next to a $ref, applied to
	// a copy of the referenced schema once every schema is lowered.
	overlays []overlay
}

type overlay struct {
	ref                *openapi3.SchemaRef
	description, title string
}

// withDescription returns ref with a description, as kin-openapi resolves a
// description written next to a $ref: on a copy of the referenced schema.
func (l *lowerer) withDescription(ref *openapi3.SchemaRef, description string) *openapi3.SchemaRef {
	return l.withOverlay(ref, overlay{description: description})
}

// withTitle returns ref with a title, like withDescription.
func (l *lowerer) withTitle(ref *openapi3.SchemaRef, title string) *openapi3.SchemaRef {
	return l.withOverlay(ref, overlay{title: title})
}

func (l *lowerer) withOverlay(ref *openapi3.SchemaRef, o overlay) *openapi3.SchemaRef {
	o.ref = &openapi3.SchemaRef{Ref: ref.Ref, Value: ref.Value, Extensions: ref.Extensions}
	if ref.Ref == "" { // inline: complete already
		o.apply()
		return o.ref
	}
	l.overlays = append(l.overlays, o)
	return o.ref
}

func (o overlay) apply() {
	if o.ref.Value == nil {
		return
	}
	v := copySchema(o.ref.Value)
	if o.description != "" {
		v.Description = o.description
	}
	if o.title != "" {
		v.Title = o.title
	}
	o.ref.Value = v
}

func (l *lowerer) body(b *BodyEntry) *openapi3.SchemaRef {
	ref := l.expr(&b.Type)
	if b.Description != "" {
		ref = l.withDescription(ref, b.Description)
	}
	return ref
}

func (l *lowerer) entry(e *TypeEntry) *openapi3.Schema {
	var s *openapi3.Schema
	switch e.TypeKind {
	case KindObject:
		s = l.object(&ObjectShape{
			Bases: e.Bases, Fields: e.Fields, AdditionalField: e.AdditionalField, Constraints: e.Constraints,
		})
	case KindUnion:
		members := e.Members
		if e.Variants.Len() > 0 {
			members = make([]TypeExpr, 0, e.Variants.Len())
			for _, k := range e.Variants.Keys {
				members = append(members, e.Variants.Values[k])
			}
		}
		s = l.union(members, e.Exclusive, e.Titles, e.BranchVersions)
		if e.Discriminator != "" {
			s.Discriminator = &openapi3.Discriminator{PropertyName: e.Discriminator}
			if e.DefaultVariant != "" {
				s.Discriminator.Extensions = map[string]any{"x-default": e.DefaultVariant}
			}
		}
	default: // alias
		switch {
		case e.GenericParameter:
			s = &openapi3.Schema{Extensions: map[string]any{"x-is-generic-type-parameter": true}}
		case e.Type != nil && e.Type.Ref != "":
			s = &openapi3.Schema{AllOf: openapi3.SchemaRefs{l.expr(e.Type)}}
		case e.Type != nil:
			s = copySchema(l.expr(e.Type).Value)
		default:
			s = &openapi3.Schema{}
		}
	}
	if e.Description != "" {
		s.Description = e.Description
	}
	setVersions(&s.Extensions, e.Versions)
	return s
}

// object lowers an object shape: its own fields (those not inherited from a
// base), one allOf $ref per base, and one union member per container or other
// union constraint.
func (l *lowerer) object(o *ObjectShape) *openapi3.Schema {
	own := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}}
	for _, name := range o.Fields.Keys {
		f := o.Fields.Values[name]
		if f.DeclaredIn != "" {
			continue
		}
		if own.Properties == nil {
			own.Properties = make(openapi3.Schemas)
		}
		own.Properties[name] = l.field(&f)
		if f.Required {
			own.Required = append(own.Required, name)
		}
	}
	if o.AdditionalField != nil && !l.baseHasAdditional(o.Bases, o.AdditionalField) {
		if o.AdditionalField.Any {
			yes := true
			own.AdditionalProperties.Has = &yes
		} else {
			own.AdditionalProperties.Schema = l.expr(o.AdditionalField)
		}
	}

	var unions openapi3.SchemaRefs
	for i := range o.Constraints {
		if u := l.constraint(&o.Constraints[i], o); u != nil {
			unions = append(unions, &openapi3.SchemaRef{Value: u})
		}
	}
	// The spec lists `required` sorted (and osgen picks union probe keys in
	// that order); the model records requiredness per field.
	sort.Strings(own.Required)
	if len(o.Bases) == 0 && len(unions) == 0 {
		return own
	}

	s := &openapi3.Schema{}
	for _, base := range o.Bases {
		s.AllOf = append(s.AllOf, l.ref(base))
	}
	if len(own.Properties) > 0 || own.AdditionalProperties.Schema != nil || own.AdditionalProperties.Has != nil {
		s.AllOf = append(s.AllOf, &openapi3.SchemaRef{Value: own})
	}
	s.AllOf = append(s.AllOf, unions...)
	return s
}

// constraint lowers a constraint that adds fields to a union member; presence
// and not_all constraints only restrict fields the object already has.
func (l *lowerer) constraint(c *Constraint, o *ObjectShape) *openapi3.Schema {
	switch {
	case c.UnionKind == "container":
		u := &openapi3.Schema{}
		for _, key := range c.Variants.Keys {
			v := c.Variants.Values[key]
			branch := &openapi3.Schema{
				Properties: openapi3.Schemas{key: l.expr(&v.Value)},
				Required:   []string{key},
			}
			for _, name := range v.ExtraFields.Keys {
				f := v.ExtraFields.Values[name]
				branch.Properties[name] = l.field(&f)
			}
			u.OneOf = append(u.OneOf, &openapi3.SchemaRef{Value: branch})
		}
		return u
	case c.Matches != "":
		u := l.union(c.Types, c.Matches == "one_of", nil, nil)
		return u
	default:
		return nil
	}
}

func (l *lowerer) union(members []TypeExpr, exclusive bool, titles []*string, versions []*Versions) *openapi3.Schema {
	refs := make(openapi3.SchemaRefs, 0, len(members))
	for i := range members {
		ref := l.expr(&members[i])
		if i < len(titles) && titles[i] != nil {
			ref = l.withTitle(ref, *titles[i])
		}
		if i < len(versions) && versions[i] != nil {
			if ref.Ref != "" {
				setVersions(&ref.Extensions, versions[i])
			} else {
				setVersions(&ref.Value.Extensions, versions[i])
			}
		}
		refs = append(refs, ref)
	}
	if exclusive {
		return &openapi3.Schema{OneOf: refs}
	}
	return &openapi3.Schema{AnyOf: refs}
}

func (l *lowerer) field(f *Field) *openapi3.SchemaRef {
	ref := l.expr(&f.Type)
	if f.Description != "" {
		ref = l.withDescription(ref, f.Description)
	}
	if f.Versions != nil { // expr and withDescription return a fresh SchemaRef
		ref.Extensions = copyMap(ref.Extensions)
		setVersions(&ref.Extensions, f.Versions)
	}
	return ref
}

// baseHasAdditional reports whether one of the bases declares the same
// additional fields, which the object then inherits rather than declares (the
// model doesn't record where additional_fields come from).
func (l *lowerer) baseHasAdditional(bases []string, additional *TypeExpr) bool {
	for _, base := range bases {
		if e := l.model.Types[base]; e != nil && e.AdditionalField != nil && reflect.DeepEqual(e.AdditionalField, additional) {
			return true
		}
	}
	return false
}

func (l *lowerer) ref(name string) *openapi3.SchemaRef {
	return &openapi3.SchemaRef{Ref: schemaRefPrefix + name, Value: l.out[name]}
}

func (l *lowerer) expr(e *TypeExpr) *openapi3.SchemaRef {
	switch {
	case e.Ref != "":
		return l.ref(e.Ref)
	case e.Scalar != "":
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{e.Scalar}, Format: e.Format}}
	case e.Enum != nil:
		s := &openapi3.Schema{Type: enumType(e.Enum), Enum: e.Enum}
		if len(e.Enum) == 1 { // the model doesn't tell `const: x` from `enum: [x]`; osgen reads const
			s.Const = e.Enum[0]
		}
		return &openapi3.SchemaRef{Value: s}
	case e.Array != nil:
		return &openapi3.SchemaRef{Value: &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeArray}, Items: l.expr(e.Array)}}
	case e.Map != nil:
		s := &openapi3.Schema{Type: &openapi3.Types{openapi3.TypeObject}}
		if e.Map.Any {
			yes := true
			s.AdditionalProperties.Has = &yes
		} else {
			s.AdditionalProperties.Schema = l.expr(e.Map)
		}
		return &openapi3.SchemaRef{Value: s}
	case e.Union != nil:
		return &openapi3.SchemaRef{Value: l.union(e.Union, e.Exclusive, e.Titles, e.BranchVersions)}
	case e.Object != nil:
		s := l.object(e.Object)
		s.Description = e.Description
		return &openapi3.SchemaRef{Value: s}
	default: // any
		return &openapi3.SchemaRef{Value: &openapi3.Schema{}}
	}
}

func copySchema(s *openapi3.Schema) *openapi3.Schema {
	c := *s
	c.Extensions = copyMap(s.Extensions)
	return &c
}

func copyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func setVersions(ext *map[string]any, v *Versions) {
	if v == nil {
		return
	}
	set := func(key, value string) {
		if value == "" {
			return
		}
		if *ext == nil {
			*ext = make(map[string]any)
		}
		(*ext)[key] = value
	}
	set("x-version-added", v.Added)
	set("x-version-removed", v.Removed)
	set("x-version-deprecated", v.Deprecated)
	set("x-deprecation-message", v.DeprecationMessage)
}

func keepExtensions(s, orig *openapi3.Schema) {
	for _, key := range keptExtensions {
		if v, ok := orig.Extensions[key]; ok {
			if s.Extensions == nil {
				s.Extensions = make(map[string]any)
			}
			s.Extensions[key] = v
		}
	}
}

// bareRefTarget is the target of a component that is a bare $ref in the
// OpenAPI file and an alias of that reference in the model.
func bareRefTarget(name string, entry *TypeEntry, original openapi3.Schemas) (string, bool) {
	orig, ok := original[name]
	if !ok || orig.Ref == "" || entry == nil || entry.TypeKind != KindAlias || entry.Type == nil || entry.Type.Ref == "" {
		return "", false
	}
	if orig.Ref != schemaRefPrefix+entry.Type.Ref {
		return "", false
	}
	return entry.Type.Ref, true
}

// keepPropertyExtensions copies the naming extensions of the original schema's
// direct properties (its own and those of its inline allOf members, e.g. a
// `status` string with x-enum-name: RestStatus) onto the lowered properties of
// the same name.
func keepPropertyExtensions(s, orig *openapi3.Schema) {
	exts := make(map[string]map[string]any)
	collect := func(o *openapi3.Schema) {
		for name, p := range o.Properties {
			if p == nil || p.Ref != "" || p.Value == nil {
				continue
			}
			for _, key := range keptExtensions {
				if v, ok := p.Value.Extensions[key]; ok {
					if exts[name] == nil {
						exts[name] = make(map[string]any)
					}
					exts[name][key] = v
				}
			}
		}
	}
	collect(orig)
	for _, m := range orig.AllOf {
		if m != nil && m.Ref == "" && m.Value != nil {
			collect(m.Value)
		}
	}
	if len(exts) == 0 {
		return
	}
	apply := func(t *openapi3.Schema) {
		for name, p := range t.Properties {
			e, ok := exts[name]
			if !ok || p == nil || p.Value == nil || p.Ref != "" {
				continue
			}
			v := copySchema(p.Value)
			if v.Extensions == nil {
				v.Extensions = make(map[string]any)
			}
			for k, x := range e {
				v.Extensions[k] = x
			}
			p.Value = v
		}
	}
	apply(s)
	for _, m := range s.AllOf {
		if m != nil && m.Ref == "" && m.Value != nil {
			apply(m.Value)
		}
	}
}

// keepBranchDescriptions copies the descriptions of a union's inline branches
// (the documentation of each const of an enum union) from the original schema;
// the model keeps branch titles and versions but not their descriptions.
func keepBranchDescriptions(s, orig *openapi3.Schema) {
	pairs := [][2]openapi3.SchemaRefs{{s.OneOf, orig.OneOf}, {s.AnyOf, orig.AnyOf}}
	for _, p := range pairs {
		if len(p[0]) == 0 || len(p[0]) != len(p[1]) {
			continue
		}
		for i, b := range p[0] {
			o := p[1][i]
			if b == nil || b.Ref != "" || b.Value == nil || o == nil || o.Value == nil || b.Value.Description != "" {
				continue
			}
			b.Value.Description = o.Value.Description
		}
	}
}

// enumType is the JSON type of an enum's values, when they share one.
func enumType(values []any) *openapi3.Types {
	t := ""
	for _, v := range values {
		var vt string
		switch x := v.(type) {
		case string:
			vt = openapi3.TypeString
		case bool:
			vt = openapi3.TypeBoolean
		case float64:
			vt = openapi3.TypeNumber
			if x == math.Trunc(x) {
				vt = openapi3.TypeInteger
			}
		case nil:
			continue
		default:
			return nil
		}
		if t != "" && t != vt {
			return nil
		}
		t = vt
	}
	if t == "" {
		return nil
	}
	return &openapi3.Types{t}
}
