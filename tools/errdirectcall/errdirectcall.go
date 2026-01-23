// Package errdirectcall provides a Go analyzer that detects when errutil.With or errutil.Wrap
// is called directly on a function call result without first checking for nil.
package errdirectcall

import (
	"go/token"
	"slices"

	"github.com/graxinc/errutil/tools/internal/shared"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const directivePrefix = "errdirectcall:unchecked"

func Analyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:     "errdirectcall",
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

	shared.ReportUnused(pass, directives, "unused errdirectcall:unchecked directive")
	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function, directives []*shared.Directive) {
	if fn.Syntax() == nil || !shared.HasErrorResult(fn.Signature) {
		return
	}
	filePos := shared.FileForPos(pass, fn.Pos())
	fnLine := pass.Fset.Position(fn.Pos()).Line

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			call, ok := instr.(*ssa.Call)
			if !ok || !call.Pos().IsValid() {
				continue
			}
			if isDirectWrapCall(call) {
				callLine := pass.Fset.Position(call.Pos()).Line
				if !shared.MarkSuppressed(directives, filePos, fnLine, callLine) {
					pass.Report(analysis.Diagnostic{
						Pos:     call.Pos(),
						Message: "do not directly wrap function calls; check for nil first",
					})
				}
			}
		}
	}
}

func isDirectWrapCall(call *ssa.Call) bool {
	if !shared.IsErrUtilWrapCall(call) || len(call.Call.Args) == 0 {
		return false
	}
	arg := call.Call.Args[0]
	switch a := arg.(type) {
	case *ssa.Call:
	case *ssa.Extract:
		if _, ok := a.Tuple.(*ssa.Call); !ok {
			return false
		}
	default:
		return false
	}
	return !isNilChecked(arg, call.Block())
}

func isNilChecked(v ssa.Value, block *ssa.BasicBlock) bool {
	visited := make(map[*ssa.BasicBlock]bool)
	var walk func(*ssa.BasicBlock) bool
	walk = func(b *ssa.BasicBlock) bool {
		if b == nil || visited[b] {
			return false
		}
		visited[b] = true
		for _, pred := range b.Preds {
			if len(pred.Instrs) == 0 {
				continue
			}
			ifInstr, ok := pred.Instrs[len(pred.Instrs)-1].(*ssa.If)
			if !ok {
				if walk(pred) {
					return true
				}
				continue
			}

			// Check for errors.Is/errors.As calls - if true branch, error is non-nil
			if call, ok := ifInstr.Cond.(*ssa.Call); ok {
				if isErrorsIsOrAs(call, v) {
					trueBlock := pred.Succs[0]
					if trueBlock == b || slices.Contains(b.Preds, trueBlock) {
						return true
					}
				}
				if walk(pred) {
					return true
				}
				continue
			}

			binOp, ok := ifInstr.Cond.(*ssa.BinOp)
			if !ok {
				if walk(pred) {
					return true
				}
				continue
			}
			var checkedVal ssa.Value
			if isNilConst(binOp.X) {
				checkedVal = binOp.Y
			} else if isNilConst(binOp.Y) {
				checkedVal = binOp.X
			}
			if checkedVal != v {
				if walk(pred) {
					return true
				}
				continue
			}
			trueBlock, falseBlock := pred.Succs[0], pred.Succs[1]
			switch binOp.Op {
			case token.NEQ:
				if trueBlock == b || slices.Contains(b.Preds, trueBlock) {
					return true
				}
			case token.EQL:
				if falseBlock == b || slices.Contains(b.Preds, falseBlock) {
					return true
				}
			}
		}
		return false
	}
	return walk(block)
}

// isErrorsIsOrAs checks if the call is errors.Is(v, ...) or errors.As(v, ...).
func isErrorsIsOrAs(call *ssa.Call, v ssa.Value) bool {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil {
		return false
	}
	if callee.Package().Pkg.Path() != "errors" {
		return false
	}
	if callee.Name() != "Is" && callee.Name() != "As" {
		return false
	}
	if len(call.Call.Args) == 0 {
		return false
	}
	return call.Call.Args[0] == v
}

func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}
