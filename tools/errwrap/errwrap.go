// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"github.com/graxinc/errutil/tools/internal/shared"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const (
	directiveUnwrapped = "errwrap:unwrapped"
	directiveNew       = "errwrap:new"
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
	unwrappedDirectives := shared.CollectDirectives(pass, directiveUnwrapped)
	newDirectives := shared.CollectDirectives(pass, directiveNew)

	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, func(fn *ssa.Function) {
		checkFunction(pass, fn, unwrappedDirectives, newDirectives)
	})

	shared.ReportUnused(pass, unwrappedDirectives, "unused errwrap:unwrapped directive")
	shared.ReportUnused(pass, newDirectives, "unused errwrap:new directive")
	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function, unwrappedDirectives, newDirectives []*shared.Directive) {
	if fn.Syntax() == nil {
		return
	}
	errIdx := shared.ErrorResultIndex(fn.Signature)
	if errIdx < 0 {
		return
	}
	filePos := shared.FileForPos(pass, fn.Pos())
	fnLine := pass.Fset.Position(fn.Pos()).Line

	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// Check for wrapping error constructors (errors.New, fmt.Errorf)
			if call, ok := instr.(*ssa.Call); ok && call.Pos().IsValid() {
				if shared.WrapsErrorConstructor(call) {
					callLine := pass.Fset.Position(call.Pos()).Line
					if !shared.MarkSuppressed(newDirectives, filePos, fnLine, callLine) {
						pass.Report(analysis.Diagnostic{
							Pos:     call.Pos(),
							Message: "use errutil.New instead of wrapping errors.New or fmt.Errorf",
						})
					}
				}
			}

			// Check for unwrapped error returns
			ret, ok := instr.(*ssa.Return)
			if !ok || !ret.Pos().IsValid() || errIdx >= len(ret.Results) {
				continue
			}
			if !isWrapped(ret.Results[errIdx], make(map[ssa.Value]bool)) {
				retLine := pass.Fset.Position(ret.Pos()).Line
				if !shared.MarkSuppressed(unwrappedDirectives, filePos, fnLine, retLine) {
					pass.Report(analysis.Diagnostic{
						Pos:     ret.Pos(),
						Message: "error should be wrapped with errutil.With or errutil.Wrap",
					})
				}
			}
		}
	}
}

func isWrapped(v ssa.Value, visited map[ssa.Value]bool) bool {
	if v == nil || visited[v] {
		return true
	}
	visited[v] = true

	switch val := v.(type) {
	case *ssa.Const:
		return val.IsNil()
	case *ssa.Call:
		return shared.IsErrUtilCall(val)
	case *ssa.Phi:
		for _, edge := range val.Edges {
			if !isWrapped(edge, visited) {
				return false
			}
		}
		return true
	case *ssa.Extract:
		if call, ok := val.Tuple.(*ssa.Call); ok {
			return shared.IsErrUtilCall(call)
		}
	case *ssa.MakeInterface:
		return isWrapped(val.X, visited)
	case *ssa.ChangeInterface:
		return isWrapped(val.X, visited)
	case *ssa.TypeAssert:
		return isWrapped(val.X, visited)
	case *ssa.UnOp:
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			return isAllocWrapped(alloc, val.Block(), visited)
		}
		return isWrapped(val.X, visited)
	}
	return false
}

func isAllocWrapped(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) bool {
	if alloc.Referrers() == nil {
		return false
	}
	if loadBlock != nil {
		for _, ref := range *alloc.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc && store.Block() == loadBlock {
				return isWrapped(store.Val, visited)
			}
		}
	}
	for _, ref := range *alloc.Referrers() {
		if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc {
			if !isWrapped(store.Val, visited) {
				return false
			}
		}
	}
	return true
}
