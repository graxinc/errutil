// Package shared provides common functionality for errutil analyzers.
package shared

import (
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ssa"
)

const ErrUtilPkg = "github.com/graxinc/errutil"

// Directive represents an analyzer directive comment.
type Directive struct {
	Pos  token.Pos
	File token.Pos
	Line int // -1 for filewide
	Used bool
}

// CollectDirectives finds all directives matching the given prefix (e.g., "errwrap:ignore").
func CollectDirectives(pass *analysis.Pass, prefix string) []*Directive {
	var directives []*Directive
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			filewide := cg.Pos() < f.Package
			for _, c := range cg.List {
				if isDirective(c.Text, prefix) {
					d := &Directive{Pos: c.Pos(), File: f.Pos(), Line: pass.Fset.Position(c.Pos()).Line}
					if filewide {
						d.Line = -1
					}
					directives = append(directives, d)
				}
			}
		}
	}
	return directives
}

// isDirective checks if a comment is a directive (starts with the prefix after //).
func isDirective(text, prefix string) bool {
	// Comment text includes the // prefix
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimLeft(text, " \t")
	return strings.HasPrefix(text, prefix)
}

// MarkSuppressed checks if a diagnostic at the given position should be suppressed.
// Returns true if suppressed, and marks the directive as used.
func MarkSuppressed(directives []*Directive, filePos token.Pos, fnLine, targetLine int) bool {
	for _, d := range directives {
		if d.File != filePos {
			continue
		}
		if d.Line < 0 || d.Line == fnLine || d.Line == fnLine-1 || d.Line == targetLine || d.Line == targetLine-1 {
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

// FileForPos returns the file position for a given position.
func FileForPos(pass *analysis.Pass, pos token.Pos) token.Pos {
	for _, f := range pass.Files {
		if f.Pos() <= pos && pos < f.End() {
			return f.Pos()
		}
	}
	if len(pass.Files) > 0 {
		return pass.Files[0].Pos()
	}
	return token.NoPos
}

// ErrorResultIndex returns the index of the error result in the signature, or -1 if none.
func ErrorResultIndex(sig *types.Signature) int {
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

// HasErrorResult returns true if the signature has an error result.
func HasErrorResult(sig *types.Signature) bool {
	return ErrorResultIndex(sig) >= 0
}

// IsErrUtilCall returns true if the call is to an errutil wrapping function.
func IsErrUtilCall(call *ssa.Call) bool {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil || callee.Package().Pkg.Path() != ErrUtilPkg {
		return false
	}
	switch callee.Name() {
	case "With", "Wrap", "Witht", "Wrapt", "New":
		return true
	}
	return false
}

// IsErrUtilWrapCall returns true if the call is to errutil.With/Wrap/Witht/Wrapt (not New).
func IsErrUtilWrapCall(call *ssa.Call) bool {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil || callee.Package().Pkg.Path() != ErrUtilPkg {
		return false
	}
	switch callee.Name() {
	case "With", "Wrap", "Witht", "Wrapt":
		return true
	}
	return false
}

// IsErrorConstructor returns true if the call is errors.New or fmt.Errorf.
func IsErrorConstructor(call *ssa.Call) bool {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil {
		return false
	}
	pkg := callee.Package().Pkg.Path()
	name := callee.Name()
	return (pkg == "errors" && name == "New") || (pkg == "fmt" && name == "Errorf")
}

// WrapsErrorConstructor returns true if the errutil.With/Wrap call wraps errors.New or fmt.Errorf.
func WrapsErrorConstructor(call *ssa.Call) bool {
	if !IsErrUtilWrapCall(call) || len(call.Call.Args) == 0 {
		return false
	}
	arg := call.Call.Args[0]
	switch a := arg.(type) {
	case *ssa.Call:
		return IsErrorConstructor(a)
	case *ssa.Extract:
		if c, ok := a.Tuple.(*ssa.Call); ok {
			return IsErrorConstructor(c)
		}
	}
	return false
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
