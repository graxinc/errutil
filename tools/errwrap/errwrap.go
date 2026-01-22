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

	// Collect all functions including anonymous ones, using a worklist algorithm
	seen := make(map[*ssa.Function]bool)
	worklist := append([]*ssa.Function{}, ssaInfo.SrcFuncs...)

	// Include package-level function members (handles function literals in var initializers)
	if ssaInfo.Pkg != nil {
		for _, mem := range ssaInfo.Pkg.Members {
			if fn, ok := mem.(*ssa.Function); ok {
				worklist = append(worklist, fn)
			}
		}
	}

	for len(worklist) > 0 {
		fn := worklist[len(worklist)-1]
		worklist = worklist[:len(worklist)-1]
		if fn == nil || seen[fn] {
			continue
		}
		seen[fn] = true
		checkFunction(pass, fn)
		worklist = append(worklist, fn.AnonFuncs...)
	}

	return nil, nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function) {
	// Check for file-level or function-level nolint
	if fn.Syntax() == nil {
		return
	}
	file := fileForPos(pass, fn.Pos())
	if file == nil || isFileNolint(file) || isNolintNear(file, pass.Fset, fn.Pos()) {
		return
	}

	// Check if function returns error
	sig := fn.Signature
	results := sig.Results()
	if results == nil {
		return
	}

	// Find which result indices are errors
	var errIndices []int
	for i := range results.Len() {
		if isErrorType(results.At(i).Type()) {
			errIndices = append(errIndices, i)
		}
	}
	if len(errIndices) == 0 {
		return
	}

	// Analyze each basic block for return instructions
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			ret, ok := instr.(*ssa.Return)
			if !ok {
				continue
			}

			retPos := ret.Pos()
			if !retPos.IsValid() {
				continue
			}
			if isNolintNear(file, pass.Fset, retPos) {
				continue
			}

			// Check each error return value
			for _, idx := range errIndices {
				if idx >= len(ret.Results) {
					continue
				}
				if msg := checkWrapped(ret.Results[idx], make(map[ssa.Value]bool)); msg != "" {
					pass.Report(analysis.Diagnostic{Pos: retPos, Message: msg})
				}
			}
		}
	}
}

const (
	msgUnwrapped  = "error should be wrapped with errutil.With or errutil.Wrap"
	msgDirectCall = "do not directly wrap function calls; check for nil first"
)

// checkWrapped returns an error message if the value is not properly wrapped, or "" if valid.
func checkWrapped(v ssa.Value, visited map[ssa.Value]bool) string {
	if v == nil {
		return ""
	}
	if visited[v] {
		return "" // cycle - already being checked, assume OK
	}
	visited[v] = true

	switch val := v.(type) {
	case *ssa.Const:
		if val.IsNil() {
			return ""
		}
		return msgUnwrapped

	case *ssa.Call:
		return checkErrUtilCall(val)

	case *ssa.Phi:
		for _, edge := range val.Edges {
			if msg := checkWrapped(edge, visited); msg != "" {
				return msg
			}
		}
		return ""

	case *ssa.Extract:
		switch tuple := val.Tuple.(type) {
		case *ssa.Call:
			return checkErrUtilCall(tuple)
		case *ssa.TypeAssert:
			return checkWrapped(tuple.X, visited)
		}
		return msgUnwrapped

	case *ssa.MakeInterface:
		return checkWrapped(val.X, visited)

	case *ssa.ChangeInterface:
		return checkWrapped(val.X, visited)

	case *ssa.UnOp:
		// Dereference - check the underlying value.
		// If it's an Alloc (used for returns when defer is present), find the store
		// in the same basic block that writes to this alloc.
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			// Find the store to this alloc in the same block as the load
			if val.Block() != nil {
				for _, instr := range val.Block().Instrs {
					store, ok := instr.(*ssa.Store)
					if ok && store.Addr == alloc {
						return checkWrapped(store.Val, visited)
					}
				}
			}
			return msgUnwrapped
		}
		return checkWrapped(val.X, visited)

	case *ssa.TypeAssert:
		return checkWrapped(val.X, visited)

	default:
		return msgUnwrapped
	}
}

// checkErrUtilCall returns an error message if the call is not a valid errutil call, or "" if valid.
func checkErrUtilCall(call *ssa.Call) string {
	callee := call.Call.StaticCallee()
	if callee == nil {
		return msgUnwrapped
	}
	pkg := callee.Package()
	if pkg == nil {
		return msgUnwrapped
	}
	if pkg.Pkg.Path() != errUtilPkg {
		return msgUnwrapped
	}

	// For With/Wrap/Witht/Wrapt, check that the error argument is not a direct function call.
	name := callee.Name()
	if name == "With" || name == "Wrap" || name == "Witht" || name == "Wrapt" {
		if len(call.Call.Args) > 0 {
			arg := call.Call.Args[0]
			if msg := checkDirectCallArg(arg, call.Block()); msg != "" {
				return msg
			}
		}
	}

	return ""
}

// checkDirectCallArg checks if the argument is a direct function call and returns an error message if problematic.
func checkDirectCallArg(v ssa.Value, block *ssa.BasicBlock) string {
	isCall := false
	switch a := v.(type) {
	case *ssa.Call:
		isCall = true
	case *ssa.Extract:
		_, isCall = a.Tuple.(*ssa.Call)
	}
	if !isCall {
		return ""
	}
	if isNilChecked(v, block, make(map[*ssa.BasicBlock]bool)) {
		return ""
	}
	return msgDirectCall
}

// isNilChecked returns true if the value has been checked for nil before reaching the given block.
// This detects patterns like: if err := f(); err != nil { return errutil.With(err) }
func isNilChecked(v ssa.Value, block *ssa.BasicBlock, visited map[*ssa.BasicBlock]bool) bool {
	if block == nil || visited[block] {
		return false
	}
	visited[block] = true

	for _, pred := range block.Preds {
		if len(pred.Instrs) == 0 {
			continue
		}

		// Check if predecessor ends with an If on our value compared to nil
		ifInstr, ok := pred.Instrs[len(pred.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		binOp, ok := ifInstr.Cond.(*ssa.BinOp)
		if !ok {
			if isNilChecked(v, pred, visited) {
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
			if isNilChecked(v, pred, visited) {
				return true
			}
			continue
		}

		// SSA If: Succs[0]=true branch, Succs[1]=false branch
		// We're in the non-nil branch if: (!= nil and in true branch) or (== nil and in false branch)
		switch binOp.Op {
		case token.NEQ:
			if pred.Succs[0] == block || slices.Contains(block.Preds, pred.Succs[0]) {
				return true
			}
		case token.EQL:
			if pred.Succs[1] == block || slices.Contains(block.Preds, pred.Succs[1]) {
				return true
			}
		}
	}

	return false
}

func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}

func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

func fileForPos(pass *analysis.Pass, pos token.Pos) *ast.File {
	for _, f := range pass.Files {
		if pass.Fset.Position(f.Pos()).Filename == pass.Fset.Position(pos).Filename {
			return f
		}
	}
	return nil
}

// isFileNolint checks if the file has a nolint:errwrap directive before the package declaration.
func isFileNolint(f *ast.File) bool {
	for _, cg := range f.Comments {
		if cg.Pos() >= f.Package {
			continue
		}
		for _, c := range cg.List {
			if strings.Contains(c.Text, "nolint:errwrap") {
				return true
			}
		}
	}
	return false
}

func isNolintNear(f *ast.File, fset *token.FileSet, pos token.Pos) bool {
	line := fset.Position(pos).Line
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			cline := fset.Position(c.Pos()).Line
			if (cline == line || cline == line-1) && strings.Contains(c.Text, "nolint:errwrap") {
				return true
			}
		}
	}
	return false
}
