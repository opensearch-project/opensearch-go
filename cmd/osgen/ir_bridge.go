// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
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
		IsPointerReq:      op.IsPointerReq,
		IsNoBody:          op.IsNoBody,
		IsPlugin:          !coreGroups[groupPrefix(op.Group)],
		ResponseRef:       op.ResponseRef,
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
		pb.Ops = append(pb.Ops, ir.PathOp{
			Kind:  convertOpKind(op.Kind),
			Value: op.Value,
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
	case opIfList:
		return ir.PathOpIfList
	case opIfStr:
		return ir.PathOpIfStr
	case opElseIfList:
		return ir.PathOpElseIfList
	case opElseIfStr:
		return ir.PathOpElseIfStr
	case opElse:
		return ir.PathOpElse
	case opEnd:
		return ir.PathOpEnd
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

	return t
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
