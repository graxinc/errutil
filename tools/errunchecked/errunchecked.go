// Package errunchecked provides a Go analyzer that detects when errutil.With or errutil.Wrap
// is called directly on a function call result without first checking for nil.
package errunchecked

import (
	"go/token"

	"github.com/graxinc/errutil/tools/internal/shared"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const directivePrefix = "errunchecked:wrap"

func Analyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:     "errunchecked",
		Doc:      "check that errutil.With/Wrap is not called directly on function results without nil check",
		Requires: []*analysis.Analyzer{buildssa.Analyzer},
		Run:      run,
	}
}

func run(pass *analysis.Pass) (any, error) {
	ssaInfo := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	directives := shared.CollectDirectives(pass, directivePrefix)

	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, func(fn *ssa.Function) {
		checkFunction(pass, fn, directives)
	})

	shared.ReportUnused(pass, directives, "unused errunchecked:wrap directive")
	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function, directives []*shared.Directive) {
	ctx, ok := shared.NewFuncContext(pass, fn)
	if !ok {
		return
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok || !call.Pos().IsValid() {
				continue
			}
			if isDirectWrapCall(call) {
				ctx.Report(directives, call.Pos(), "do not directly wrap function calls; check for nil first")
			}
		}
	}
}

func isDirectWrapCall(call *ssa.Call) bool {
	if !shared.IsErrUtilWrapCall(call) || len(call.Call.Args) == 0 {
		return false
	}
	arg := call.Call.Args[0]
	inner, ok := shared.UnderlyingCall(arg)
	if !ok {
		return false
	}
	// errutil wrap/constructor functions and errors.New/fmt.Errorf never return a
	// nil error, so wrapping their result needs no nil check. (A nested wrap thus
	// flags only the inner call, on the raw function result.)
	if shared.IsErrUtilCall(inner) || shared.IsErrorConstructor(inner) {
		return false
	}
	return !isNilChecked(arg, call.Block())
}

// isNilChecked reports whether v is guaranteed non-nil at block. This holds when
// a conditional that establishes v's nil-ness (a v != nil / v == nil test, an
// equality against a sentinel, or errors.Is/As on v) guards block via establishes.
func isNilChecked(v ssa.Value, block *ssa.BasicBlock) bool {
	for _, b := range block.Parent().Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		trueBlock, falseBlock := b.Succs[0], b.Succs[1]

		if call, ok := ifInstr.Cond.(*ssa.Call); ok {
			if isErrorsIsOrAs(call, v) && establishes(trueBlock, block) {
				return true
			}
			continue
		}

		binOp, ok := ifInstr.Cond.(*ssa.BinOp)
		if !ok || (binOp.Op != token.EQL && binOp.Op != token.NEQ) {
			continue
		}
		var other ssa.Value
		switch {
		case binOp.X == v:
			other = binOp.Y
		case binOp.Y == v:
			other = binOp.X
		default:
			continue
		}
		equalBlock, notEqualBlock := trueBlock, falseBlock
		if binOp.Op == token.NEQ {
			equalBlock, notEqualBlock = falseBlock, trueBlock
		}
		// v != nil establishes non-nil on the not-equal branch. v == sentinel
		// establishes it on the equal branch (a nil sentinel is treated as a
		// deliberate check too; that case is rare and indistinguishable statically).
		nonNilBlock := equalBlock
		if isNilConst(other) {
			nonNilBlock = notEqualBlock
		}
		if establishes(nonNilBlock, block) {
			return true
		}
	}
	return false
}

// establishes reports whether a property that holds on branch is guaranteed to
// hold at block. branch must dominate block, and branch must have a single
// predecessor so it is entered only via its conditional edge (see isNilChecked).
func establishes(branch, block *ssa.BasicBlock) bool {
	return len(branch.Preds) == 1 && branch.Dominates(block)
}

// isErrorsIsOrAs checks if the call is errors.Is(v, target) or errors.As(v, ...)
// in a form that, when true, implies v is non-nil. errors.Is(v, nil) is excluded
// because it is true exactly when v is nil.
func isErrorsIsOrAs(call *ssa.Call, v ssa.Value) bool {
	pkg, name, ok := shared.CalleeInfo(call)
	if !ok || pkg != "errors" || (name != "Is" && name != "As") {
		return false
	}
	args := call.Call.Args
	if len(args) == 0 || args[0] != v {
		return false
	}
	// errors.Is(v, nil) is true only when v IS nil. (errors.As's target is a
	// non-nil pointer, so it has no equivalent case.)
	if name == "Is" && len(args) >= 2 && isNilConst(args[1]) {
		return false
	}
	return true
}

func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}
