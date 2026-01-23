// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
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
	ruleUnwrapped  = "unwrapped"
	ruleDirectCall = "directcall"
	ruleUnused     = "unused"
)

var ruleMessages = map[string]string{
	ruleUnwrapped:  "error should be wrapped with errutil.With or errutil.Wrap",
	ruleDirectCall: "do not directly wrap function calls; check for nil first",
	ruleUnused:     "unused errwrap directive",
}

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
	directives := collectDirectives(pass)

	seen := make(map[*ssa.Function]bool)
	var check func(*ssa.Function)
	check = func(fn *ssa.Function) {
		if fn == nil || seen[fn] {
			return
		}
		seen[fn] = true
		checkFunction(pass, fn, directives)
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

	for _, d := range directives {
		if !d.used {
			pass.Report(analysis.Diagnostic{Pos: d.pos, Message: ruleMessages[ruleUnused]})
		}
	}
	return nil, nil
}

type directive struct {
	pos  token.Pos
	file token.Pos
	line int // -1 for filewide
	rules []string
	used  bool
}

func collectDirectives(pass *analysis.Pass) []*directive {
	var directives []*directive
	for _, f := range pass.Files {
		for _, cg := range f.Comments {
			filewide := cg.Pos() < f.Package
			for _, c := range cg.List {
				if d := parseDirective(c.Text, c.Pos(), pass.Fset.Position(c.Pos()).Line, filewide, f.Pos()); d != nil {
					directives = append(directives, d)
				}
			}
		}
	}
	return directives
}

func parseDirective(text string, pos token.Pos, line int, filewide bool, filePos token.Pos) *directive {
	idx := strings.Index(text, "errwrap:")
	if idx < 0 {
		return nil
	}
	rule := text[idx+len("errwrap:"):]
	if i := strings.IndexAny(rule, " \t"); i >= 0 {
		rule = rule[:i]
	}
	d := &directive{pos: pos, file: filePos, line: line}
	if filewide {
		d.line = -1
	}
	switch rule {
	case "ignore":
		return d
	case ruleUnwrapped, ruleDirectCall:
		d.rules = []string{rule}
		return d
	}
	return nil
}

func checkFunction(pass *analysis.Pass, fn *ssa.Function, directives []*directive) {
	if fn.Syntax() == nil {
		return
	}
	errIdx := errorResultIndex(fn.Signature)
	if errIdx < 0 {
		return
	}
	filePos := pass.Files[0].Pos()
	for _, f := range pass.Files {
		if f.Pos() <= fn.Pos() && fn.Pos() < f.End() {
			filePos = f.Pos()
			break
		}
	}
	fnLine := pass.Fset.Position(fn.Pos()).Line
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			ret, ok := instr.(*ssa.Return)
			if !ok || !ret.Pos().IsValid() || errIdx >= len(ret.Results) {
				continue
			}
			if rule := checkValue(ret.Results[errIdx], make(map[ssa.Value]bool)); rule != "" {
				retLine := pass.Fset.Position(ret.Pos()).Line
				if !markSuppressed(directives, filePos, fnLine, retLine, rule) {
					pass.Report(analysis.Diagnostic{Pos: ret.Pos(), Message: ruleMessages[rule]})
				}
			}
		}
	}
}

func markSuppressed(directives []*directive, filePos token.Pos, fnLine, retLine int, rule string) bool {
	for _, d := range directives {
		if d.file != filePos {
			continue
		}
		if d.line < 0 || d.line == fnLine || d.line == fnLine-1 || d.line == retLine || d.line == retLine-1 {
			if len(d.rules) == 0 || slices.Contains(d.rules, rule) {
				d.used = true
				return true
			}
		}
	}
	return false
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
			if rule := checkValue(edge, visited); rule != "" {
				return rule
			}
		}
		return ""
	case *ssa.Extract:
		if call, ok := val.Tuple.(*ssa.Call); ok {
			return checkCall(call)
		}
	case *ssa.MakeInterface:
		return checkValue(val.X, visited)
	case *ssa.ChangeInterface:
		return checkValue(val.X, visited)
	case *ssa.TypeAssert:
		return checkValue(val.X, visited)
	case *ssa.UnOp:
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			return checkAlloc(alloc, val.Block(), visited)
		}
		return checkValue(val.X, visited)
	}
	return ruleUnwrapped
}

func checkAlloc(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) string {
	if alloc.Referrers() == nil {
		return ruleUnwrapped
	}
	if loadBlock != nil {
		for _, ref := range *alloc.Referrers() {
			if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc && store.Block() == loadBlock {
				return checkValue(store.Val, visited)
			}
		}
	}
	for _, ref := range *alloc.Referrers() {
		if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc {
			if rule := checkValue(store.Val, visited); rule != "" {
				return rule
			}
		}
	}
	return ""
}

func checkCall(call *ssa.Call) string {
	callee := call.Call.StaticCallee()
	if callee == nil || callee.Package() == nil || callee.Package().Pkg.Path() != errUtilPkg {
		return ruleUnwrapped
	}
	switch callee.Name() {
	case "With", "Wrap", "Witht", "Wrapt":
		if len(call.Call.Args) > 0 {
			return checkDirectCall(call.Call.Args[0], call.Block())
		}
	}
	return ""
}

func checkDirectCall(v ssa.Value, block *ssa.BasicBlock) string {
	switch a := v.(type) {
	case *ssa.Call:
	case *ssa.Extract:
		if _, ok := a.Tuple.(*ssa.Call); !ok {
			return ""
		}
	default:
		return ""
	}
	if isNilChecked(v, block) {
		return ""
	}
	return ruleDirectCall
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

func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}
