// Package shared provides common functionality for errutil analyzers.
package shared

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

const ErrUtilPkg = "github.com/graxinc/errutil"

// Directive represents an analyzer directive comment.
type Directive struct {
	Pos   token.Pos
	File  *token.File
	Line  int       // -1 for filewide
	Owner token.Pos // Pos of the syntax node of the function this directive belongs to; NoPos if none/filewide.
	Used  bool
}

// CollectDirectives finds all directives matching the given prefix (e.g., "errwrap:ignore").
func CollectDirectives(pass *analysis.Pass, prefix string) []*Directive {
	var directives []*Directive
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			filewide := cg.Pos() < f.Package
			for _, c := range cg.List {
				if isDirective(c.Text, prefix) {
					line := pass.Fset.Position(c.Pos()).Line
					d := &Directive{Pos: c.Pos(), File: pass.Fset.File(c.Pos()), Line: line}
					if filewide {
						d.Line = -1
					} else {
						d.Owner = ownerFunc(pass, f, cg, line)
					}
					directives = append(directives, d)
				}
			}
		}
	}
	return directives
}

// ownerFunc returns the Pos of the function a directive belongs to: the function
// whose doc comment is the directive, or the innermost function whose line span
// contains the directive. Returns NoPos if the directive is not associated with a function.
func ownerFunc(pass *analysis.Pass, f *ast.File, cg *ast.CommentGroup, line int) token.Pos {
	owner := token.NoPos
	ast.Inspect(f, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Doc == cg {
				owner = fn.Pos()
				return false
			}
			if funcLineSpanContains(pass, fn, line) {
				owner = fn.Pos()
			}
		case *ast.FuncLit:
			if funcLineSpanContains(pass, fn, line) {
				owner = fn.Pos()
			}
		}
		return true
	})
	return owner
}

func funcLineSpanContains(pass *analysis.Pass, n ast.Node, line int) bool {
	start := pass.Fset.Position(n.Pos()).Line
	end := pass.Fset.Position(n.End()).Line
	return line >= start && line <= end
}

// isDirective checks if a comment is a directive (starts with the prefix after //).
func isDirective(text, prefix string) bool {
	// Comment text includes the // marker.
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimLeft(text, " \t")
	rest, ok := strings.CutPrefix(text, prefix)
	if !ok {
		return false
	}
	// Require a word boundary so "errwrap:unwrapped" does not match
	// "errwrap:unwrappedtypo": the prefix must be the whole token, followed by
	// end-of-comment or a non-identifier character (whitespace, arguments, etc.).
	return rest == "" || !isIdentChar(rest[0])
}

func isIdentChar(b byte) bool {
	return b == '_' ||
		'a' <= b && b <= 'z' ||
		'A' <= b && b <= 'Z' ||
		'0' <= b && b <= '9'
}

// FuncContext carries the per-function state needed to report suppressible
// diagnostics. Build one per SSA function with NewFuncContext.
type FuncContext struct {
	pass   *analysis.Pass
	file   *token.File
	fnPos  token.Pos // Pos of the function's syntax node, for matching directive ownership.
	fnLine int
}

// NewFuncContext builds the reporting context for fn. ok is false when fn has no
// syntax (e.g. a synthetic wrapper), in which case there is nothing to report on.
func NewFuncContext(pass *analysis.Pass, fn *ssa.Function) (ctx FuncContext, ok bool) {
	if fn.Syntax() == nil {
		return FuncContext{}, false
	}
	return FuncContext{
		pass:   pass,
		file:   pass.Fset.File(fn.Pos()),
		fnPos:  fn.Syntax().Pos(),
		fnLine: pass.Fset.Position(fn.Pos()).Line,
	}, true
}

// Report emits a diagnostic at pos with the given message, unless one of the
// directives suppresses it (in which case that directive is marked used).
func (c FuncContext) Report(directives []*Directive, pos token.Pos, message string) {
	line := c.pass.Fset.Position(pos).Line
	if !c.suppressed(directives, line) {
		c.pass.Report(analysis.Diagnostic{Pos: pos, Message: message})
	}
}

func (c FuncContext) suppressed(directives []*Directive, targetLine int) bool {
	for _, d := range directives {
		if d.File != c.file {
			continue
		}
		if d.Line < 0 { // filewide
			d.Used = true
			return true
		}
		// A line-level directive only suppresses diagnostics in the function it belongs to.
		if d.Owner != c.fnPos {
			continue
		}
		// A directive sits on, or one line above, either the function declaration
		// (suppressing the whole function) or the specific diagnostic line.
		near := func(line int) bool { return d.Line == line || d.Line == line-1 }
		if near(c.fnLine) || near(targetLine) {
			d.Used = true
			return true
		}
	}
	return false
}

// ReportUnused reports all unused directives.
func ReportUnused(pass *analysis.Pass, directives []*Directive, message string) {
	for _, d := range directives {
		if !d.Used {
			pass.Report(analysis.Diagnostic{Pos: d.Pos, Message: message})
		}
	}
}

// CalleeInfo returns the package path and name of the call's static callee.
// ok is false if the callee is not statically known (e.g. an interface method).
func CalleeInfo(call *ssa.Call) (pkgPath, name string, ok bool) {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil {
		return "", "", false
	}
	return callee.Package().Pkg.Path(), callee.Name(), true
}

// UnderlyingCall returns the *ssa.Call that produced v, whether v is that call
// directly or an extraction of one of its results. ok is false otherwise.
func UnderlyingCall(v ssa.Value) (call *ssa.Call, ok bool) {
	switch val := v.(type) {
	case *ssa.Call:
		return val, true
	case *ssa.Extract:
		if c, ok := val.Tuple.(*ssa.Call); ok {
			return c, true
		}
	}
	return nil, false
}

var errType = types.Universe.Lookup("error").Type()

// ErrorResultIndices returns the indices of all error results in the signature.
func ErrorResultIndices(sig *types.Signature) []int {
	results := sig.Results() // nil-safe: (*types.Tuple).Len reports 0 for a nil tuple.
	var indices []int
	for i := range results.Len() {
		if types.Identical(results.At(i).Type(), errType) {
			indices = append(indices, i)
		}
	}
	return indices
}

// errUtilWrapFuncs is the set of errutil functions that wrap an existing error.
// errutil.New constructs a new error and is therefore not included.
var errUtilWrapFuncs = map[string]bool{"With": true, "Wrap": true, "Witht": true, "Wrapt": true}

// isErrUtilCall reports whether call targets an errutil wrapping function, or
// errutil.New when allowNew is set.
func isErrUtilCall(call *ssa.Call, allowNew bool) bool {
	pkg, name, ok := CalleeInfo(call)
	if !ok || pkg != ErrUtilPkg {
		return false
	}
	return errUtilWrapFuncs[name] || (allowNew && name == "New")
}

// IsErrUtilCall returns true if the call is to an errutil wrapping function or errutil.New.
func IsErrUtilCall(call *ssa.Call) bool { return isErrUtilCall(call, true) }

// IsErrUtilWrapCall returns true if the call is to errutil.With/Wrap/Witht/Wrapt (not New).
func IsErrUtilWrapCall(call *ssa.Call) bool { return isErrUtilCall(call, false) }

// IsErrorConstructor returns true if the call is errors.New or fmt.Errorf.
func IsErrorConstructor(call *ssa.Call) bool {
	pkg, name, ok := CalleeInfo(call)
	return ok && ((pkg == "errors" && name == "New") || (pkg == "fmt" && name == "Errorf"))
}

// WrapsErrorConstructor returns true if the errutil.With/Wrap call wraps errors.New or fmt.Errorf.
func WrapsErrorConstructor(call *ssa.Call) bool {
	if !IsErrUtilWrapCall(call) || len(call.Call.Args) == 0 {
		return false
	}
	c, ok := UnderlyingCall(call.Call.Args[0])
	return ok && IsErrorConstructor(c)
}

// WalkFunctions walks all functions in the SSA including anonymous functions.
func WalkFunctions(ssaInfo *ssa.Package, srcFuncs []*ssa.Function, fn func(*ssa.Function)) {
	seen := make(map[*ssa.Function]bool)
	var walk func(*ssa.Function)
	walk = func(f *ssa.Function) {
		if f == nil || seen[f] {
			return
		}
		seen[f] = true
		fn(f)
		for _, anon := range f.AnonFuncs {
			walk(anon)
		}
	}
	for _, f := range srcFuncs {
		walk(f)
	}
	if ssaInfo != nil {
		for _, mem := range ssaInfo.Members {
			if f, ok := mem.(*ssa.Function); ok {
				walk(f)
			}
		}
	}
}
