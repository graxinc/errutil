// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const errUtilPkg = "github.com/graxinc/errutil"

const (
	msgUnwrapped  = "error should be wrapped with errutil.With or errutil.Wrap"
	msgDirectCall = "do not directly wrap function calls; check for nil first"
)

func Analyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:     "errwrap",
		Doc:      "check that errors are wrapped with errutil.With or errutil.Wrap",
		Requires: []*analysis.Analyzer{buildssa.Analyzer},
		Run:      run,
	}
}

func run(pass *analysis.Pass) (any, error) {
	ssaInfo := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)

	seen := make(map[*ssa.Function]bool)
	var check func(fn *ssa.Function)
	check = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		checkFunction(pass, fn)
		for _, anon := range fn.AnonFuncs {
			check(anon)
		}
	}

	for _, fn := range ssaInfo.SrcFuncs {
		check(fn)
	}
	if ssaInfo.Pkg != nil {
		for _, mem := range ssaInfo.Pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				check(fn)
			}
		}
	}
	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function) {
	if fn.Syntax() == nil {
		return
	}
	file := fileForPos(pass, fn.Pos())
	if file == nil || hasNolint(file, pass.Fset, fn.Pos()) {
		return
	}

	errIdx := errorResultIndex(fn.Signature)
	if errIdx < 0 {
		return
	}

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			ret, ok := instr.(*ssa.Return)
			if !ok || !ret.Pos().IsValid() {
				continue
			}
			if hasNolint(file, pass.Fset, ret.Pos()) {
				continue
			}
			if errIdx < len(ret.Results) {
				if msg := checkValue(ret.Results[errIdx], make(map[ssa.Value]bool)); msg != "" {
					pass.Report(analysis.Diagnostic{Pos: ret.Pos(), Message: msg})
				}
			}
		}
	}
}

func errorResultIndex(sig *types.Signature) int {
	results := sig.Results()
	if results == nil {
		return -1
	}
	errType := types.Universe.Lookup("error").Type()
	for i := range results.Len() {
		if types.Identical(results.At(i).Type(), errType) {
			return i
		}
	}
	return -1
}

// checkValue returns an error message if the value is not properly wrapped.
func checkValue(v ssa.Value, visited map[ssa.Value]bool) string {
	if v == nil || visited[v] {
		return ""
	}
	visited[v] = true

	switch val := v.(type) {
	case *ssa.Const:
		if val.IsNil() {
			return ""
		}
	case *ssa.Call:
		return checkCall(val)
	case *ssa.Phi:
		for _, edge := range val.Edges {
			if msg := checkValue(edge, visited); msg != "" {
				return msg
			}
		}
		return ""
	case *ssa.Extract:
		if call, ok := val.Tuple.(*ssa.Call); ok {
			return checkCall(call)
		}
	case *ssa.MakeInterface, *ssa.ChangeInterface, *ssa.TypeAssert:
		return checkValue(operand(val), visited)
	case *ssa.UnOp:
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			return checkAlloc(alloc, val.Block(), visited)
		}
		return checkValue(val.X, visited)
	}
	return msgUnwrapped
}

func operand(v ssa.Value) ssa.Value {
	switch val := v.(type) {
	case *ssa.MakeInterface:
		return val.X
	case *ssa.ChangeInterface:
		return val.X
	case *ssa.TypeAssert:
		return val.X
	}
	return nil
}

func checkAlloc(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) string {
	if alloc.Referrers() == nil {
		return msgUnwrapped
	}
	// First check for a store in the same block as the load (handles defer)
	if loadBlock != nil {
		for _, ref := range *alloc.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc && store.Block() == loadBlock {
				return checkValue(store.Val, visited)
			}
		}
	}
	// Then check all stores (handles range-over-func)
	for _, ref := range *alloc.Referrers() {
		if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc {
			if msg := checkValue(store.Val, visited); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func checkCall(call *ssa.Call) string {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil {
		return msgUnwrapped
	}
	if callee.Package().Pkg.Path() != errUtilPkg {
		return msgUnwrapped
	}

	name := callee.Name()
	if name == "With" || name == "Wrap" || name == "Witht" || name == "Wrapt" {
		if len(call.Call.Args) > 0 {
			if msg := checkDirectCall(call.Call.Args[0], call.Block()); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func checkDirectCall(v ssa.Value, block *ssa.BasicBlock) string {
	var call *ssa.Call
	switch a := v.(type) {
	case *ssa.Call:
		call = a
	case *ssa.Extract:
		c, ok := a.Tuple.(*ssa.Call)
		if !ok {
			return ""
		}
		call = c
	default:
		return ""
	}
	_ = call // the value is a call

	if isNilChecked(v, block) {
		return ""
	}
	return msgDirectCall
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
			binOp, ok := ifInstr.Cond.(*ssa.BinOp)
			if !ok {
				if walk(pred) {
					return true
				}
				continue
			}

			// Check if comparing our value to nil
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

			// Check we're in the non-nil branch
			trueBlock, falseBlock := pred.Succs[0], pred.Succs[1]
			switch binOp.Op {
			case token.NEQ: // err != nil, true branch is non-nil
				if trueBlock == b || slices.Contains(b.Preds, trueBlock) {
					return true
				}
			case token.EQL: // err == nil, false branch is non-nil
				if falseBlock == b || slices.Contains(b.Preds, falseBlock) {
					return true
				}
			}
		}
		return false
	}
	return walk(block)
}


func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}

func fileForPos(pass *analysis.Pass, pos token.Pos) *ast.File {
	for _, f := range pass.Files {
		if pass.Fset.Position(f.Pos()).Filename == pass.Fset.Position(pos).Filename {
			return f
		}
	}
	return nil
}

func hasNolint(f *ast.File, fset *token.FileSet, pos token.Pos) bool {
	line := fset.Position(pos).Line
	for _, cg := range f.Comments {
		// File-level nolint (before package)
		if cg.Pos() < f.Package {
			for _, c := range cg.List {
				if strings.Contains(c.Text, "nolint:errwrap") {
					return true
				}
			}
			continue
		}
		// Line-level nolint
		for _, c := range cg.List {
			cline := fset.Position(c.Pos()).Line
			if (cline == line || cline == line-1) && strings.Contains(c.Text, "nolint:errwrap") {
				return true
			}
		}
	}
	return false
}
