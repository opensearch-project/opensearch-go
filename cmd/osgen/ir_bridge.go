// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/token"
	"strings"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/errwrap"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// convertToIR translates the legacy apiOperation slice and typeRegistry into
// the new IR representation. This is the bridge between the existing extraction
// pipeline and the new emit phase.
func convertToIR(ops []apiOperation, reg *typeRegistry) *ir.Spec {
	spec := &ir.Spec{
		Registry: ir.NewTypeRegistry(reg.CorePkg, reg.CoreImport),
	}

	for i := range ops {
		spec.Operations = append(spec.Operations, convertOperation(&ops[i]))
	}

	for _, gt := range reg.all() {
		irType := convertType(gt)
		spec.Types = append(spec.Types, irType)
		spec.Registry.Register(irType)
	}

	classifyUnions(spec)

	return spec
}

func convertOperation(op *apiOperation) *ir.Operation {
	irOp := &ir.Operation{
		Group:             op.Group,
		TypePrefix:        op.TypePrefix,
		Description:       op.Description,
		VersionAdded:      op.VersionAdded,
		VersionDeprecated: op.VersionDeprecated,
		Deprecated:        op.Deprecated,
		DeprecationMsg:    op.DeprecationMsg,
		DocsURL:           op.DocsURL,
		ExcludedDistros:   op.ExcludedDistros,
		HTTPMethods:       op.HTTPMethods,
		PrimaryPath:       op.PrimaryPath,
		HasBody:           op.HasBody,
		IsNDJSON:          op.IsNDJSON,
		IsPointerReq:      op.IsPointerReq,
		IsNoBody:          op.IsNoBody,
		IsPlugin:          !coreGroups[groupPrefix(op.Group)],
		ResponseRef:       op.ResponseRef,
		ErrorWrappers:     resolveErrorWrappers(op),
	}

	// Plugin operations dispatch through their own flat client method; its
	// name shares the core acronym/idiomatic-abbreviation rules via
	// methodNameFromSuffix. Core operations carry names on DispatchRoutes.
	if irOp.IsPlugin {
		irOp.MethodName = methodNameFromSuffix(pluginGroupSuffix(op.Group))
	}

	// Build a set of required path param names from the path builder.
	requiredParams := make(map[string]bool, len(op.PathBuilder.Fields))
	for _, bf := range op.PathBuilder.Fields {
		if bf.Required {
			requiredParams[bf.Name] = true
		}
	}

	for _, f := range op.PathFields {
		irOp.PathFields = append(irOp.PathFields, ir.PathField{
			GoName:   f.GoName,
			WireName: f.WireName,
			IsList:   f.IsList,
			Required: requiredParams[f.GoName],
		})
	}

	for _, p := range op.QueryParams {
		irOp.QueryParams = append(irOp.QueryParams, convertQueryParam(p))
	}

	irOp.PathBuilder = convertPathBuilder(op.PathBuilder)

	for _, dr := range op.DispatchRoutes {
		fieldPath := ""
		if !dr.TopLevel {
			fieldPath = resolveFieldPath(dr.ReceiverType)
		}
		irOp.DispatchRoutes = append(irOp.DispatchRoutes, ir.DispatchRoute{
			ReceiverType: dr.ReceiverType,
			MethodName:   dr.MethodName,
			FieldPath:    fieldPath,
			TopLevel:     dr.TopLevel,
			Deprecated:   dr.Deprecated,
			Forward:      dr.Forward,
		})
	}

	// Convert sibling types.
	for _, st := range op.SiblingTypes {
		irOp.SiblingTypes = append(irOp.SiblingTypes, convertType(st))
	}

	// Convert response fields into a Type if present, or create an empty
	// response type for non-NoBody operations (the template always adds the
	// private response field).
	if !op.IsNoBody {
		respType := &ir.Type{
			Name:       op.TypePrefix + "Resp",
			SchemaRef:  op.ResponseRef,
			Kind:       ir.TypeStruct,
			Scope:      ir.ScopeResponse,
			OwnerGroup: op.Group,
		}
		for _, f := range op.RespFields {
			respType.Fields = append(respType.Fields, convertField(f))
		}
		irOp.Response = respType
	}

	// Response shape (map/array/raw) and element type.
	irOp.RespShape = op.RespShape
	if op.RespElemType != nil {
		irOp.RespElemType = convertType(op.RespElemType)
	}

	// Convert request body type.
	if op.HasTypedBody && len(op.ReqBodyFields) > 0 {
		name := op.ReqBodyTypeName
		if name == "" {
			name = op.TypePrefix + emit.BodySuffix
		}
		scope := ir.ScopeLocal
		if op.ReqBodyIsShared {
			scope = ir.ScopeShared
		}
		bodyType := &ir.Type{
			Name:       name,
			SchemaRef:  op.RequestRef,
			Kind:       ir.TypeStruct,
			Scope:      scope,
			OwnerGroup: op.Group,
		}
		for _, f := range op.ReqBodyFields {
			bodyType.Fields = append(bodyType.Fields, convertField(f))
		}
		irOp.RequestBody = bodyType
		irOp.HasTypedBody = true
	}

	for _, st := range op.ReqBodySiblings {
		irOp.ReqBodySiblings = append(irOp.ReqBodySiblings, convertType(st))
	}

	return irOp
}

func convertQueryParam(p apiQueryParam) ir.QueryParam {
	return ir.QueryParam{
		GoName:            p.GoName,
		WireName:          p.ParamName,
		Description:       p.Description,
		Default:           p.Default,
		GoType:            p.GoType,
		Kind:              classifyParamKind(p),
		Group:             sharedParamGroup(p.ParamName),
		Required:          p.Required,
		Deprecated:        p.Deprecated,
		VersionAdded:      p.VersionAdded,
		VersionDeprecated: p.VersionDeprecated,
		DeprecationMsg:    p.DeprecationMsg,
	}
}

func classifyParamKind(p apiQueryParam) ir.ParamKind {
	switch {
	case p.IsDuration:
		return ir.ParamDuration
	case p.IsBool:
		return ir.ParamBool
	case p.IsInt:
		return ir.ParamInt
	case p.IsList:
		return ir.ParamList
	default:
		return ir.ParamString
	}
}

func convertPathBuilder(b builder) ir.PathBuilder {
	pb := ir.PathBuilder{
		StructName: b.StructName,
	}
	for _, f := range b.Fields {
		pb.Fields = append(pb.Fields, ir.PathBuilderField{
			Name:     f.Name,
			Param:    f.Param,
			Required: f.Required,
			IsList:   f.IsList,
		})
	}
	for _, op := range b.Ops {
		conds := make([]ir.PathCaseCondition, 0, len(op.Conditions))
		for _, c := range op.Conditions {
			conds = append(conds, ir.PathCaseCondition{Field: c.Field, IsList: c.IsList})
		}
		pb.Ops = append(pb.Ops, ir.PathOp{
			Kind:       convertOpKind(op.Kind),
			Value:      op.Value,
			Conditions: conds,
		})
	}
	for _, dep := range b.PositionalDeps {
		pb.PositionalDeps = append(pb.PositionalDeps, ir.PathPositionalDep{
			Dependent: ir.PathBuilderField{
				Name:     dep.Dependent.Name,
				Param:    dep.Dependent.Param,
				Required: dep.Dependent.Required,
				IsList:   dep.Dependent.IsList,
			},
			Predecessor: ir.PathBuilderField{
				Name:     dep.Predecessor.Name,
				Param:    dep.Predecessor.Param,
				Required: dep.Predecessor.Required,
				IsList:   dep.Predecessor.IsList,
			},
		})
	}
	return pb
}

func convertOpKind(k opKind) ir.PathOpKind {
	switch k {
	case opLit:
		return ir.PathOpLit
	case opField:
		return ir.PathOpField
	case opList:
		return ir.PathOpList
	case opSwitch:
		return ir.PathOpSwitch
	case opCase:
		return ir.PathOpCase
	case opDefault:
		return ir.PathOpDefault
	case opSwitchEnd:
		return ir.PathOpSwitchEnd
	case opIf:
		return ir.PathOpIf
	case opIfEnd:
		return ir.PathOpIfEnd
	case opExplainCheck:
		return ir.PathOpExplainCheck
	default:
		return ir.PathOpLit
	}
}

func convertType(gt *goType) *ir.Type {
	t := &ir.Type{
		Name:      gt.Name,
		SchemaRef: gt.SchemaRef,
		Comment:   gt.Comment,
		Package:   gt.Pkg,
	}

	switch {
	case gt.IsEnum:
		t.Kind = ir.TypeEnum
	case gt.IsUnion && gt.IsLazy:
		t.Kind = ir.TypeLazyUnion
	case gt.IsUnion:
		t.Kind = ir.TypeUnion
	default:
		t.Kind = ir.TypeStruct
	}

	switch {
	case gt.IsShared:
		t.Scope = ir.ScopeShared
	case gt.IsResp:
		t.Scope = ir.ScopeResponse
	default:
		t.Scope = ir.ScopeLocal
	}

	for _, f := range gt.Fields {
		t.Fields = append(t.Fields, convertField(f))
	}

	for _, b := range gt.Branches {
		t.Branches = append(t.Branches, convertUnionBranch(b))
	}

	t.EnumMembers = convertEnumMembers(gt.Name, gt.EnumValues)

	return t
}

// convertEnumMembers pairs each enum wire value with its generated Go const
// identifier (<TypeName><PascalValue>), reusing the shared acronym-aware
// segment titling so values like HTTP_VERSION_NOT_SUPPORTED render correctly.
//
// It panics if a value yields an invalid Go identifier (e.g. an empty value, or
// one whose segments produce nothing) or if two distinct values collapse to the
// same const name (e.g. "FOO_BAR" and "FOO__BAR"). Both would otherwise emit
// uncompilable Go — failing loudly at generation time matches osgen's handling
// of other invalid-identifier cases (see naming.go).
func convertEnumMembers(typeName string, values []string) []ir.EnumMember {
	if len(values) == 0 {
		return nil
	}
	members := make([]ir.EnumMember, 0, len(values))
	// Seed with the reserved names a member must not collide with: the type
	// name itself and the generated <Type>Unknown sentinel. An empty or
	// otherwise-degenerate value could produce either and yield uncompilable Go.
	seen := map[string]string{
		typeName:             "<type name>",
		typeName + "Unknown": "<unknown sentinel>",
	}
	for _, v := range values {
		constName := typeName + enumValueIdent(v)
		if !token.IsIdentifier(constName) {
			panic(fmt.Sprintf("convertEnumMembers: enum value %q on type %q produced invalid Go const identifier %q",
				v, typeName, constName))
		}
		if prev, dup := seen[constName]; dup {
			panic(fmt.Sprintf("convertEnumMembers: enum values %q and %q on type %q both map to const %q",
				prev, v, typeName, constName))
		}
		seen[constName] = v
		members = append(members, ir.EnumMember{ConstName: constName, Value: v})
	}
	return members
}

// enumValueIdent converts an enum wire value (typically SCREAMING_SNAKE_CASE)
// to a PascalCase identifier segment, expanding known acronyms (HTTP, URI, ...)
// via titleSegment. e.g. "NOT_FOUND" -> "NotFound", "HTTP_VERSION_NOT_SUPPORTED"
// -> "HTTPVersionNotSupported".
func enumValueIdent(value string) string {
	var sb strings.Builder
	for _, part := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '_' || r == '.' || r == '-'
	}) {
		sb.WriteString(titleSegment(strings.ToLower(part)))
	}
	return sb.String()
}

func convertField(f goField) ir.Field {
	return ir.Field{
		GoName:            f.GoName,
		JSONName:          f.JSONName,
		GoType:            f.GoType,
		IsPointer:         f.IsPointer,
		IsEmbed:           f.IsEmbed,
		OmitEmpty:         f.OmitEmpty,
		Comment:           f.Comment,
		VersionAdded:      f.VersionAdded,
		VersionDeprecated: f.VersionDeprecated,
		DeprecationMsg:    f.DeprecationMsg,
	}
}

func convertUnionBranch(b unionBranch) ir.UnionBranch {
	return ir.UnionBranch{
		Name:         b.Name,
		GoType:       b.GoType,
		TokenClass:   convertTokenClass(b.TokenClass),
		Required:     b.Required,
		IsRef:        b.IsRef,
		VersionAdded: b.VersionAdded,
	}
}

func convertTokenClass(tc string) ir.TokenClass {
	switch tc {
	case "object":
		return ir.TokenObject
	case "array":
		return ir.TokenArray
	case "string":
		return ir.TokenString
	case "number":
		return ir.TokenNumber
	case "bool":
		return ir.TokenBool
	default:
		return ir.TokenObject
	}
}

// resolveErrorWrappers returns the partial-failure wrapper-schema names
// for an operation. The bundled spec ships with the proposed
// x-error-responses extension; until upstream merges, we fall back to
// the local errwrap catalog so plugin operations and other ops that
// haven't been annotated still get coverage when the local hardcoded
// map knows about them.
func resolveErrorWrappers(op *apiOperation) []string {
	if len(op.ErrorWrappers) > 0 {
		return op.ErrorWrappers
	}
	return errwrap.For(op.Group)
}
