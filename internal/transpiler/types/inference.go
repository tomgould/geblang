package types

import "geblang/internal/ast"

func InferLiteral(expr ast.Expression) *Type {
	switch expr.(type) {
	case *ast.IntegerLiteral:
		return &Type{Kind: KindInt}
	case *ast.FloatLiteral:
		return &Type{Kind: KindFloat}
	case *ast.DecimalLiteral:
		return &Type{Kind: KindDecimal}
	case *ast.StringLiteral, *ast.InterpolatedString:
		return &Type{Kind: KindString}
	}
	return Unknown()
}

