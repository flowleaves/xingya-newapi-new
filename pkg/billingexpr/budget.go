package billingexpr

import (
	"fmt"
	"math"

	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
)

type costInterval struct{ lo, hi float64 }

// MaximumFixedRequestCost accepts only request-priced leaves, including every
// unselected branch. Token-dependent tier conditions may select another fixed
// fee at settlement, so reserve the highest fee before submitting upstream.
func MaximumFixedRequestCost(expression string) (float64, error) {
	if _, err := CompileFromCache(expression); err != nil {
		return 0, err
	}
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return 0, err
	}
	if err = validateRequestPriceLeaves(tree.Node, 0); err != nil {
		return 0, err
	}
	return MaximumTokenCost(expression, 0, 0)
}

func validateRequestPriceLeaves(node ast.Node, depth int) error {
	if depth > 64 {
		return fmt.Errorf("request pricing expression is too deep")
	}
	switch n := node.(type) {
	case *ast.ConditionalNode:
		if err := validateRequestPriceLeaves(n.Exp1, depth+1); err != nil {
			return err
		}
		return validateRequestPriceLeaves(n.Exp2, depth+1)
	case *ast.BinaryNode:
		if n.Operator == "*" {
			if isRequestPriceMultiplier(n.Right) {
				return validateRequestPriceLeaves(n.Left, depth+1)
			}
			if isRequestPriceMultiplier(n.Left) {
				return validateRequestPriceLeaves(n.Right, depth+1)
			}
		}
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if ok && callee.Value == "tier" && len(n.Arguments) == 2 {
			fixed, ok := n.Arguments[1].(*ast.CallNode)
			if ok {
				callee, ok = fixed.Callee.(*ast.IdentifierNode)
				if ok && callee.Value == "fixed" && len(fixed.Arguments) == 1 {
					return nil // CompileFromCache already validates literal prices.
				}
			}
		}
	}
	return fmt.Errorf("all pricing branches must use tier(name, fixed(price))")
}

// MaximumTokenCost bounds all token categories and both arms of every tier.
// Unknown numeric request probes or functions are rejected, never sampled to
// guess an upper bound. It does not alter the expression used for settlement.
func MaximumTokenCost(expression string, maxInput, maxOutput float64) (float64, error) {
	if maxInput < 0 || maxOutput < 0 || math.IsInf(maxInput, 0) || math.IsInf(maxOutput, 0) || math.IsNaN(maxInput) || math.IsNaN(maxOutput) {
		return 0, fmt.Errorf("invalid token budget")
	}
	_, body := ParseExprVersion(expression)
	tree, err := parser.Parse(body)
	if err != nil {
		return 0, err
	}
	rangeCost, err := boundCost(tree.Node, maxInput, maxOutput, 0)
	if err != nil {
		return 0, err
	}
	if math.IsInf(rangeCost.hi, 0) || math.IsNaN(rangeCost.hi) || rangeCost.hi < 0 {
		return 0, fmt.Errorf("unbounded billing cost")
	}
	return rangeCost.hi, nil
}

func boundCost(node ast.Node, input, output float64, depth int) (costInterval, error) {
	if depth > 64 {
		return costInterval{}, fmt.Errorf("billing budget expression is too deep")
	}
	depth++
	switch n := node.(type) {
	case *ast.IntegerNode:
		return costInterval{float64(n.Value), float64(n.Value)}, nil
	case *ast.FloatNode:
		return costInterval{n.Value, n.Value}, nil
	case *ast.IdentifierNode:
		switch n.Value {
		case "p", "len", "cr", "cc", "cc1h", "img", "img_cr", "ai":
			return costInterval{0, input}, nil
		case "c", "img_o", "ao":
			return costInterval{0, output}, nil
		case "image_count":
			return costInterval{1, 1}, nil
		}
	case *ast.ConditionalNode:
		a, err := boundCost(n.Exp1, input, output, depth)
		if err != nil {
			return a, err
		}
		b, err := boundCost(n.Exp2, input, output, depth)
		if err != nil {
			return b, err
		}
		return costInterval{math.Min(a.lo, b.lo), math.Max(a.hi, b.hi)}, nil
	case *ast.UnaryNode:
		a, err := boundCost(n.Node, input, output, depth)
		if err != nil {
			return a, err
		}
		if n.Operator == "-" {
			return costInterval{-a.hi, -a.lo}, nil
		}
		if n.Operator == "+" {
			return a, nil
		}
	case *ast.BinaryNode:
		a, err := boundCost(n.Left, input, output, depth)
		if err != nil {
			return a, err
		}
		b, err := boundCost(n.Right, input, output, depth)
		if err != nil {
			return b, err
		}
		switch n.Operator {
		case "+":
			return costInterval{a.lo + b.lo, a.hi + b.hi}, nil
		case "-":
			return costInterval{a.lo - b.hi, a.hi - b.lo}, nil
		case "*":
			products := []float64{a.lo * b.lo, a.lo * b.hi, a.hi * b.lo, a.hi * b.hi}
			lo, hi := products[0], products[0]
			for _, p := range products[1:] {
				lo = math.Min(lo, p)
				hi = math.Max(hi, p)
			}
			return costInterval{lo, hi}, nil
		case "/":
			if b.lo == b.hi && b.lo > 0 {
				return costInterval{a.lo / b.lo, a.hi / b.lo}, nil
			}
		}
	case *ast.CallNode:
		callee, ok := n.Callee.(*ast.IdentifierNode)
		if !ok {
			break
		}
		if callee.Value == "tier" && len(n.Arguments) == 2 {
			return boundCost(n.Arguments[1], input, output, depth)
		}
		if callee.Value == "fixed" && len(n.Arguments) == 1 {
			a, err := boundCost(n.Arguments[0], input, output, depth)
			return costInterval{a.lo * 1e6, a.hi * 1e6}, err
		}
		return boundMath(callee.Value, n.Arguments, input, output, depth)
	case *ast.BuiltinNode:
		return boundMath(n.Name, n.Arguments, input, output, depth)
	}
	return costInterval{}, fmt.Errorf("billing expression cannot be bounded: %s", node.String())
}

func boundMath(name string, args []ast.Node, input, output float64, depth int) (costInterval, error) {
	if len(args) < 1 || len(args) > 2 {
		return costInterval{}, fmt.Errorf("unsupported budget function %s", name)
	}
	a, err := boundCost(args[0], input, output, depth)
	if err != nil {
		return a, err
	}
	if len(args) == 1 {
		switch name {
		case "ceil":
			return costInterval{math.Ceil(a.lo), math.Ceil(a.hi)}, nil
		case "floor":
			return costInterval{math.Floor(a.lo), math.Floor(a.hi)}, nil
		case "abs":
			lo := math.Min(math.Abs(a.lo), math.Abs(a.hi))
			if a.lo <= 0 && a.hi >= 0 {
				lo = 0
			}
			return costInterval{lo, math.Max(math.Abs(a.lo), math.Abs(a.hi))}, nil
		}
	}
	if len(args) == 2 {
		b, err := boundCost(args[1], input, output, depth)
		if err != nil {
			return b, err
		}
		if name == "max" {
			return costInterval{math.Max(a.lo, b.lo), math.Max(a.hi, b.hi)}, nil
		}
		if name == "min" {
			return costInterval{math.Min(a.lo, b.lo), math.Min(a.hi, b.hi)}, nil
		}
	}
	return costInterval{}, fmt.Errorf("unsupported budget function %s", name)
}
