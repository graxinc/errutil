// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const errUtilPkg = "github.com/graxinc/errutil"

func Analyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:     "errwrap",
		Doc:      "check that errors are wrapped with errutil.With or errutil.Wrap",
		Requires: []*analysis.Analyzer{inspect.Analyzer},
		Run:      run,
	}
}

func run(pass *analysis.Pass) (any, error) {
	insp, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, nil
	}

	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		var fnType *ast.FuncType
		var body *ast.BlockStmt
		var funcPos token.Pos
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body == nil {
				return
			}
			fnType, body, funcPos = fn.Type, fn.Body, fn.Pos()
		case *ast.FuncLit:
			fnType, body, funcPos = fn.Type, fn.Body, fn.Pos()
		}

		file := fileForPos(pass, funcPos)
		if file == nil || isFileNolint(file) || isNolintNear(file, pass.Fset, funcPos) {
			return
		}

		errIndices, namedErrVars := errorReturnInfo(pass, fnType)
		if len(errIndices) == 0 {
			return
		}

		// Find unwrapped assignments to named error variables
		unwrappedAssignments := findUnwrappedAssignments(pass, body, namedErrVars)

		ast.Inspect(body, func(n ast.Node) bool {
			if _, ok := n.(*ast.FuncLit); ok {
				return false
			}
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}

			// Handle bare return (no explicit results)
			if len(ret.Results) == 0 && len(namedErrVars) > 0 {
				// Report all unwrapped assignments before this return (conservative approach
				// since we don't do control flow analysis)
				if !isNolintNear(file, pass.Fset, ret.Pos()) {
					for _, pos := range unwrappedAssignments {
						if pos < ret.Pos() {
							pass.Reportf(ret.Pos(), "bare return with named error return; error assigned at line %d should be wrapped",
								pass.Fset.Position(pos).Line)
						}
					}
				}
				return true
			}

			// Handle explicit return values
			for _, idx := range errIndices {
				if idx < len(ret.Results) && !isWrapped(pass, ret.Results[idx]) && !isNolintNear(file, pass.Fset, ret.Results[idx].Pos()) {
					pass.Reportf(ret.Results[idx].Pos(), "error should be wrapped with errutil.With or errutil.Wrap")
				}
			}
			return true
		})
	})

	return nil, nil
}

// findUnwrappedAssignments finds assignments to named error variables that are not wrapped.
func findUnwrappedAssignments(pass *analysis.Pass, body *ast.BlockStmt, namedErrVars []string) []token.Pos {
	if len(namedErrVars) == 0 {
		return nil
	}

	varSet := make(map[string]struct{}, len(namedErrVars))
	for _, name := range namedErrVars {
		varSet[name] = struct{}{}
	}

	var positions []token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false // Don't descend into nested function literals
		}
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok {
				continue
			}
			if _, isErrVar := varSet[ident.Name]; isErrVar && i < len(assign.Rhs) && !isWrapped(pass, assign.Rhs[i]) {
				positions = append(positions, assign.Pos())
			}
		}
		return true
	})
	return positions
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

// errorReturnInfo returns the indices of all error returns and the names of named error return variables.
func errorReturnInfo(pass *analysis.Pass, fnType *ast.FuncType) (indices []int, namedVars []string) {
	if fnType.Results == nil {
		return nil, nil
	}
	idx := 0
	for _, field := range fnType.Results.List {
		t := pass.TypesInfo.TypeOf(field.Type)
		isError := t != nil && t.String() == "error"
		count := max(len(field.Names), 1)
		if isError {
			for i := range count {
				indices = append(indices, idx+i)
			}
			for _, name := range field.Names {
				namedVars = append(namedVars, name.Name)
			}
		}
		idx += count
	}
	return indices, namedVars
}

func isWrapped(pass *analysis.Pass, expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	pkgName, ok := pass.TypesInfo.Uses[ident].(*types.PkgName)
	return ok && pkgName.Imported().Path() == errUtilPkg
}
