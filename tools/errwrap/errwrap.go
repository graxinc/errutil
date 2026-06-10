// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"go/token"

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

	generated := shared.GeneratedFiles(pass)
	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, func(fn *ssa.Function) {
		if shared.IsGeneratedFunc(fn, pass.Fset, generated) {
			return
		}
		checkFunction(pass, fn, unwrappedDirectives, newDirectives)
	})

	shared.ReportUnused(pass, unwrappedDirectives, "unused errwrap:unwrapped directive")
	shared.ReportUnused(pass, newDirectives, "unused errwrap:new directive")
	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function, unwrappedDirectives, newDirectives []*shared.Directive) {
	ctx, ok := shared.NewFuncContext(pass, fn)
	if !ok {
		return
	}
	errIndices := shared.ErrorResultIndices(fn.Signature)
	var ends map[token.Pos]token.Pos // built lazily; most functions report nothing
	endOf := func(pos token.Pos) token.Pos {
		if ends == nil {
			ends = shared.SubjectEnds(fn)
		}
		return ends[pos]
	}
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// Check for wrapping error constructors (errors.New, fmt.Errorf).
			// CallInstruction covers ordinary calls as well as defer and go
			// statements, whose call wraps a constructor just the same.
			if call, ok := instr.(ssa.CallInstruction); ok && call.Pos().IsValid() {
				if shared.WrapsErrorConstructor(call) {
					ctx.ReportRange(newDirectives, call.Pos(), endOf(call.Pos()),
						"use errutil.New instead of wrapping errors.New or fmt.Errorf")
				}
			}

			// Check for unwrapped error returns
			ret, ok := instr.(*ssa.Return)
			if !ok || !ret.Pos().IsValid() {
				continue
			}
			for _, errIdx := range errIndices {
				if errIdx >= len(ret.Results) {
					continue
				}
				if !isWrapped(ret.Results[errIdx], make(map[ssa.Value]bool)) {
					ctx.ReportRange(unwrappedDirectives, ret.Pos(), endOf(ret.Pos()),
						"error should be wrapped with errutil.With or errutil.Wrap")
				}
			}
		}
	}
}

func isWrapped(v ssa.Value, visited map[ssa.Value]bool) bool {
	// A nil value is a nil error (fine to return unwrapped); an already-visited
	// value means we are following a cycle (e.g. a phi feeding itself), which we
	// treat as wrapped so the recursion terminates without a false positive.
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
		if call, ok := shared.UnderlyingCall(val); ok {
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

// deferredStoresWrapped inspects deferred closures in alloc's function that
// capture alloc and store to it. found is false when no such store exists.
// Otherwise wrapped reports whether every such store is wrapped. Only defers
// guaranteed to be registered before the load (their block dominates the load's
// block) are considered: a conditionally registered defer cannot vouch for
// every path.
func deferredStoresWrapped(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) (wrapped, found bool) {
	wrapped = true
	for _, b := range alloc.Parent().Blocks {
		for _, instr := range b.Instrs {
			d, ok := instr.(*ssa.Defer)
			if !ok || !d.Block().Dominates(loadBlock) {
				continue
			}
			mc, ok := d.Call.Value.(*ssa.MakeClosure)
			if !ok {
				continue
			}
			closure := mc.Fn.(*ssa.Function)
			for i, binding := range mc.Bindings {
				if binding != alloc {
					continue
				}
				for _, ref := range shared.Referrers(closure.FreeVars[i]) {
					store, ok := ref.(*ssa.Store)
					if !ok {
						continue
					}
					found = true
					wrapped = wrapped && isWrapped(store.Val, visited)
				}
			}
		}
	}
	return wrapped, found
}

func isAllocWrapped(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) bool {
	if alloc.Referrers() == nil {
		return false
	}
	// A deferred closure that stores into alloc (a captured named result) runs
	// after every return statement, so its stores determine the value callers
	// observe, overriding any store made before the return — e.g. the idiomatic
	// `defer func() { err = errutil.With(err) }()`.
	if wrapped, found := deferredStoresWrapped(alloc, loadBlock, visited); found {
		return wrapped
	}
	// If the load's block contains stores, the last one (in instruction order)
	// dominates the load, so only its value matters — earlier stores are overwritten.
	if loadBlock != nil {
		var lastStore *ssa.Store
		for _, instr := range loadBlock.Instrs {
			if store, ok := instr.(*ssa.Store); ok && store.Addr == alloc {
				lastStore = store
			}
		}
		if lastStore != nil {
			return isWrapped(lastStore.Val, visited)
		}
	}
	// Fallback: check all stores across all blocks.
	for _, ref := range *alloc.Referrers() {
		if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc {
			if !isWrapped(store.Val, visited) {
				return false
			}
		}
	}
	return true
}
