// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"go/ast"
	"go/token"
	"go/types"
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
				if !isWrapped(ret.Results[idx], make(map[ssa.Value]bool)) {
					pass.Reportf(retPos, "error should be wrapped with errutil.With or errutil.Wrap")
				}
			}
		}
	}
}

// isWrapped returns true if the value is properly wrapped (nil or from errutil).
func isWrapped(v ssa.Value, visited map[ssa.Value]bool) bool {
	if v == nil || visited[v] {
		return v == nil
	}
	visited[v] = true

	switch val := v.(type) {
	case *ssa.Const:
		return val.IsNil()

	case *ssa.Call:
		return isErrUtilCall(val)

	case *ssa.Phi:
		for _, edge := range val.Edges {
			if !isWrapped(edge, visited) {
				return false
			}
		}
		return true

	case *ssa.Extract:
		switch tuple := val.Tuple.(type) {
		case *ssa.Call:
			return isErrUtilCall(tuple)
		case *ssa.TypeAssert:
			return isWrapped(tuple.X, visited)
		}
		return false

	case *ssa.MakeInterface:
		return isWrapped(val.X, visited)

	case *ssa.ChangeInterface:
		return isWrapped(val.X, visited)

	case *ssa.UnOp:
		// Check if this is a dereference of an Alloc (named return variable).
		// If so, trace the values stored into the alloc.
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			return isAllocWrapped(alloc, visited)
		}
		return isWrapped(val.X, visited)

	case *ssa.TypeAssert:
		return isWrapped(val.X, visited)

	default:
		return false
	}
}

// isAllocWrapped checks if all values stored into an Alloc are wrapped.
// This handles named return variables where the value is stored and then loaded.
func isAllocWrapped(alloc *ssa.Alloc, visited map[ssa.Value]bool) bool {
	// Find all stores to this alloc by checking its referrers
	refs := alloc.Referrers()
	if refs == nil {
		return false
	}

	for _, ref := range *refs {
		store, ok := ref.(*ssa.Store)
		if !ok {
			continue
		}
		// Check if this store is writing to our alloc
		if store.Addr != alloc {
			continue
		}
		// Check if the stored value is wrapped
		if !isWrapped(store.Val, visited) {
			return false
		}
	}
	return true
}

// isErrUtilCall checks if an SSA Call instruction is a call to an errutil function.
func isErrUtilCall(call *ssa.Call) bool {
	callee := call.Call.StaticCallee()
	if callee == nil {
		return false
	}
	pkg := callee.Package()
	if pkg == nil {
		return false
	}
	return pkg.Pkg.Path() == errUtilPkg
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
