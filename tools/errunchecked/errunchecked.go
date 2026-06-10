// Package errunchecked provides a Go analyzer that detects when errutil.With or errutil.Wrap
// is called directly on a function call result without first checking for nil.
package errunchecked

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"slices"

	"github.com/graxinc/errutil/tools/internal/shared"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const directivePrefix = "errutil:unchecked"

func Analyzer() *analysis.Analyzer {
	return &analysis.Analyzer{
		Name:      "errunchecked",
		Doc:       "check that errutil.With/Wrap is not called directly on function results without nil check",
		Requires:  []*analysis.Analyzer{buildssa.Analyzer},
		FactTypes: []analysis.Fact{(*nonNilError)(nil)},
		Run:       run,
	}
}

func run(pass *analysis.Pass) (any, error) {
	ssaInfo, ok := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	if !ok {
		return nil, fmt.Errorf("unexpected buildssa result type %T", pass.ResultOf[buildssa.Analyzer])
	}
	directives := shared.CollectDirectives(pass, directivePrefix)

	c := &checker{pass: pass, memo: map[funcResult]bool{}, inProgress: map[funcResult]bool{}}

	// Export "always returns non-nil error" facts for this package's functions so
	// that importing packages can recognize them (cross-package callees have no SSA
	// body to inspect). This also warms the memo for the check phase below.
	// Generated functions still export facts — they can vouch for handwritten
	// callers — but are not checked below.
	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, c.exportNonNilFact)

	generated := shared.GeneratedFiles(pass)
	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, func(fn *ssa.Function) {
		if shared.IsGeneratedFunc(fn, pass.Fset, generated) {
			return
		}
		c.checkFunction(fn, directives)
	})

	shared.ReportUnused(pass, directives, "unused errutil:unchecked directive")
	return nil, nil
}

// checker holds per-package state for the analysis: the pass, plus a memo and
// in-progress set for the (potentially recursive) non-nil-return computation.
type checker struct {
	pass       *analysis.Pass
	memo       map[funcResult]bool
	inProgress map[funcResult]bool
}

func (c *checker) checkFunction(fn *ssa.Function, directives []*shared.Directive) {
	ctx, ok := shared.NewFuncContext(c.pass, fn)
	if !ok {
		return
	}
	var callEnds map[token.Pos]token.Pos // built lazily; most functions report nothing
	for _, block := range fn.Blocks {
		for _, instr := range block.Instrs {
			// CallInstruction covers ordinary calls as well as defer and go
			// statements, whose call (and its arguments) is just as unchecked.
			call, ok := instr.(ssa.CallInstruction)
			if !ok || !call.Pos().IsValid() {
				continue
			}
			if c.isDirectWrapCall(call) {
				if callEnds == nil {
					callEnds = shared.SubjectEnds(fn)
				}
				ctx.ReportRange(directives, call.Pos(), callEnds[call.Pos()],
					"do not directly wrap function calls; check for nil first")
			}
		}
	}
}

func (c *checker) isDirectWrapCall(call ssa.CallInstruction) bool {
	args := call.Common().Args
	if !shared.IsErrUtilWrapCall(call) || len(args) == 0 {
		return false
	}
	// Interface-to-interface conversions preserve nil-ness exactly, so the
	// wrapped value is in scope only if a call underlies the conversion chain.
	base := args[0]
	for src := conversionSource(base); src != nil; src = conversionSource(src) {
		base = src
	}
	inner, ok := shared.UnderlyingCall(base)
	if !ok {
		return false
	}
	// context.Context.Err() is non-nil once the context is done, so wrapping it
	// inside a <-ctx.Done() branch is safe.
	if isContextErrInDoneBranch(inner, call.Block()) {
		return false
	}
	return !c.provedNonNil(args[0], call.Block())
}

// provedNonNil reports whether v is provably non-nil at block: constructed
// non-nil (errutil/standard constructors, or a callee whose every return is a
// non-nil error — proved from its body, or imported as a fact cross-package),
// or guarded by a dominating nil check. Nil-ness-preserving interface
// conversions are looked through: a proof on any value in the chain suffices.
func (c *checker) provedNonNil(v ssa.Value, block *ssa.BasicBlock) bool {
	for ; v != nil; v = conversionSource(v) {
		if c.valueNonNil(v) || isNilChecked(v, block) {
			return true
		}
	}
	return false
}

// isContextErrInDoneBranch reports whether call is ctx.Err() and some block that
// establishes the context is done dominates block — i.e. the wrap sits on a path
// taken only after the context is done, where ctx.Err() is guaranteed non-nil.
func isContextErrInDoneBranch(call *ssa.Call, block *ssa.BasicBlock) bool {
	recv, ok := contextMethodRecv(call, "Err")
	if !ok {
		return false
	}
	for _, done := range doneEstablishingBlocks(block.Parent(), recv) {
		if done.Dominates(block) {
			return true
		}
	}
	return false
}

// doneEstablishingBlocks returns the blocks after which recv (a context) is
// guaranteed done: the block of a bare <-recv.Done() receive (done holds for the
// rest of that block onward), and the handler block of a select's <-recv.Done()
// arm. Crucially a select establishes done only inside its Done arm, not in the
// select block itself (which dominates every arm, including ones reached while
// the context is not yet done).
func doneEstablishingBlocks(fn *ssa.Function, recv ssa.Value) []*ssa.BasicBlock {
	var blocks []*ssa.BasicBlock
	for _, b := range fn.Blocks {
		for _, instr := range b.Instrs {
			switch v := instr.(type) {
			case *ssa.UnOp:
				if v.Op == token.ARROW && isContextDoneCall(v.X, recv) {
					blocks = append(blocks, b)
				}
			case *ssa.Select:
				for i, st := range v.States {
					if st.Dir != types.RecvOnly || !isContextDoneCall(st.Chan, recv) {
						continue
					}
					if h := selectCaseBlock(v, i); h != nil {
						blocks = append(blocks, h)
					}
				}
			}
		}
	}
	return blocks
}

// selectCaseBlock returns the handler block for the given state index of a
// select. The SSA builder dispatches a select by comparing the chosen state
// index (extract #0 of the select tuple) against each case index with an `if`;
// the matching if's true successor is that case's handler. It returns nil,
// defensively, if the dispatch does not match this shape — none is known: a
// single-case select without default compiles to a plain channel receive and
// never produces a Select at all (the UnOp path in doneEstablishingBlocks
// handles it), and every Select the builder emits uses this dispatch.
func selectCaseBlock(sel *ssa.Select, stateIdx int) *ssa.BasicBlock {
	for _, ext := range shared.Referrers(sel) {
		extract, ok := ext.(*ssa.Extract)
		if !ok || extract.Index != 0 { // extract #0 is the chosen state index
			continue
		}
		for _, use := range shared.Referrers(extract) {
			binOp, ok := use.(*ssa.BinOp)
			if !ok || binOp.Op != token.EQL {
				continue
			}
			// One operand is extract (we are iterating its referrers); require the
			// other to be the constant stateIdx.
			if !isIntConst(binOp.X, stateIdx) && !isIntConst(binOp.Y, stateIdx) {
				continue
			}
			for _, cond := range shared.Referrers(binOp) {
				if ifInstr, ok := cond.(*ssa.If); ok && ifInstr.Cond == binOp {
					return ifInstr.Block().Succs[0] // Succs[0] is the true branch.
				}
			}
		}
	}
	return nil
}

func isIntConst(v ssa.Value, n int) bool {
	c, ok := v.(*ssa.Const)
	if !ok || c.Value == nil {
		return false
	}
	i, exact := constant.Int64Val(c.Value)
	return exact && i == int64(n)
}

func isContextDoneCall(v ssa.Value, recv ssa.Value) bool {
	c, ok := v.(*ssa.Call)
	if !ok {
		return false
	}
	r, ok := contextMethodRecv(c, "Done")
	return ok && sameContext(r, recv)
}

// sameContext reports whether a and b denote the same context: identical SSA
// values, or accesses of the same field path from the same base (each access of
// s.ctx is a distinct SSA load/field instruction, so plain value identity would
// miss the common `<-s.ctx.Done(); s.ctx.Err()` pattern). Two loads of the same
// address could in principle observe different values if the field were
// reassigned between them, but a context stored in a field is overwhelmingly
// write-once, so they are treated as equal.
func sameContext(a, b ssa.Value) bool {
	if a == b {
		return true
	}
	switch av := a.(type) {
	case *ssa.UnOp:
		bv, ok := b.(*ssa.UnOp)
		return ok && av.Op == token.MUL && bv.Op == token.MUL && sameContext(av.X, bv.X)
	case *ssa.Field:
		bv, ok := b.(*ssa.Field)
		return ok && av.Field == bv.Field && sameContext(av.X, bv.X)
	case *ssa.FieldAddr:
		bv, ok := b.(*ssa.FieldAddr)
		return ok && av.Field == bv.Field && sameContext(av.X, bv.X)
	}
	return false
}

// contextMethodRecv returns the receiver of an invoked context.Context method of
// the given name (e.g. Err, Done).
func contextMethodRecv(call *ssa.Call, name string) (recv ssa.Value, ok bool) {
	c := call.Call
	if !c.IsInvoke() || c.Method == nil || c.Method.Name() != name {
		return nil, false
	}
	if c.Method.Pkg() == nil || c.Method.Pkg().Path() != "context" {
		return nil, false
	}
	return c.Value, true
}

// nonNilError is a fact attached to a function whose error result at each listed
// index is non-nil on every return path. It lets importing packages recognize a
// callee as never-nil even though its body is not available cross-package.
type nonNilError struct {
	Indices []int
}

func (*nonNilError) AFact() {}

func (f *nonNilError) String() string { return fmt.Sprintf("nonNilError%v", f.Indices) }

type funcResult struct {
	fn  *ssa.Function
	idx int
}

// exportNonNilFact computes and exports a nonNilError fact for fn, if any of its
// error results are always non-nil.
func (c *checker) exportNonNilFact(fn *ssa.Function) {
	obj := fn.Object()
	// Only exported functions and methods of this package can be referenced (and
	// have their fact imported) by another package; same-package callees are
	// proved directly from their body, so a fact for an unexported function
	// would never be used. Exported() is name-based, which is what we want: an
	// exported method on an unexported type is still callable cross-package
	// (e.g. on a value obtained from an exported function).
	if obj == nil || obj.Pkg() != c.pass.Pkg || !obj.Exported() {
		return
	}
	sig, ok := obj.Type().(*types.Signature)
	if !ok {
		return
	}
	var indices []int
	for _, i := range shared.ErrorResultIndices(sig) {
		if c.funcResultNonNil(fn, i) {
			indices = append(indices, i)
		}
	}
	if len(indices) > 0 {
		c.pass.ExportObjectFact(obj, &nonNilError{Indices: indices})
	}
}

// valueNonNil reports whether v is a provably non-nil error: an interface
// construction (which is never nil, even when boxing a nil pointer), a known
// non-nil constructor, or a call whose callee always returns a non-nil error.
func (c *checker) valueNonNil(v ssa.Value) bool {
	if src := conversionSource(v); src != nil {
		return c.valueNonNil(src)
	}
	switch val := v.(type) {
	case *ssa.MakeInterface:
		return true
	case *ssa.Call:
		if shared.IsErrUtilCall(val) || shared.IsErrorConstructor(val) {
			return true
		}
		return c.calleeResultNonNil(val.Call.StaticCallee(), 0)
	case *ssa.Extract:
		if call, ok := val.Tuple.(*ssa.Call); ok {
			return c.calleeResultNonNil(call.Call.StaticCallee(), val.Index)
		}
	}
	return false
}

// calleeResultNonNil reports whether callee's result at idx is always a non-nil
// error. For callees with a body (this package) it inspects the returns; for
// callees without one (another package) it consults an imported fact.
func (c *checker) calleeResultNonNil(callee *ssa.Function, idx int) bool {
	if callee == nil {
		return false
	}
	if len(callee.Blocks) > 0 {
		return c.funcResultNonNil(callee, idx)
	}
	obj := callee.Object()
	if obj == nil {
		return false
	}
	var fact nonNilError
	return c.pass.ImportObjectFact(obj, &fact) && slices.Contains(fact.Indices, idx)
}

// funcResultNonNil reports whether fn's result at idx is non-nil on every return
// path, memoized across the package. Recursion is treated conservatively (a cycle
// yields false), as is a function with no body.
func (c *checker) funcResultNonNil(fn *ssa.Function, idx int) bool {
	if len(fn.Blocks) == 0 {
		return false
	}
	key := funcResult{fn, idx}
	if v, ok := c.memo[key]; ok {
		return v
	}
	if c.inProgress[key] {
		return false
	}
	c.inProgress[key] = true
	res := c.computeResultNonNil(fn, idx)
	delete(c.inProgress, key)
	c.memo[key] = res
	return res
}

func (c *checker) computeResultNonNil(fn *ssa.Function, idx int) bool {
	sawReturn := false
	for _, b := range fn.Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		ret, ok := b.Instrs[len(b.Instrs)-1].(*ssa.Return)
		if !ok {
			continue
		}
		sawReturn = true
		// A nil-check-guarded return — the `if err != nil { return err }`
		// shape — counts via provedNonNil, letting a helper vouch for an
		// unprovable callee's result (e.g. one routed through a replaceable
		// function variable) by guarding it.
		if idx >= len(ret.Results) || !c.provedNonNil(ret.Results[idx], b) {
			return false
		}
	}
	return sawReturn
}

// conversionSource returns the source value of a nil-ness-preserving interface
// conversion, or nil if v is not one. The builder emits ChangeInterface when
// the method sets differ and ChangeType when they are identical (e.g. a
// defined interface that only embeds error); both convert nil to nil and
// non-nil to non-nil.
func conversionSource(v ssa.Value) ssa.Value {
	switch v := v.(type) {
	case *ssa.ChangeInterface:
		return v.X
	case *ssa.ChangeType:
		return v.X
	}
	return nil
}

// nilCheckEquivalent reports whether a nil-ness check of a also establishes the
// nil-ness of b. Identical values trivially qualify. Distinct ctx.Err() calls
// on the same context qualify too: Err is monotone ("After Err returns a
// non-nil error, successive calls to Err return the same error"), so a call
// observed non-nil in a dominating branch means a later call returns that same
// non-nil error.
func nilCheckEquivalent(a, b ssa.Value) bool {
	if a == b {
		return true
	}
	ac, ok := a.(*ssa.Call)
	if !ok {
		return false
	}
	bc, ok := b.(*ssa.Call)
	if !ok {
		return false
	}
	ar, ok := contextMethodRecv(ac, "Err")
	if !ok {
		return false
	}
	br, ok := contextMethodRecv(bc, "Err")
	return ok && sameContext(ar, br)
}

// isNilChecked reports whether v is guaranteed non-nil at block. This holds when
// a conditional that establishes v's nil-ness (a v != nil / v == nil test, an
// equality against a sentinel, errors.Is/As on v, or a comma-ok type assertion
// on v) guards block via establishes.
func isNilChecked(v ssa.Value, block *ssa.BasicBlock) bool {
	for _, b := range block.Parent().Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		trueBlock, falseBlock := b.Succs[0], b.Succs[1]

		switch cond := ifInstr.Cond.(type) {
		case *ssa.Call:
			if isErrorsIsOrAs(cond, v) && establishes(trueBlock, block) {
				return true
			}
		case *ssa.Extract:
			// The ok of a comma-ok type assertion on v: a nil interface never
			// asserts successfully to any type, so ok being true implies v is
			// non-nil. This also covers the if-chain a type switch lowers to
			// (its `case nil` arm lowers to a nil comparison, handled below).
			ta, isAssert := cond.Tuple.(*ssa.TypeAssert)
			if isAssert && cond.Index == 1 && ta.CommaOk && nilCheckEquivalent(ta.X, v) && establishes(trueBlock, block) {
				return true
			}
		case *ssa.BinOp:
			if cond.Op != token.EQL && cond.Op != token.NEQ {
				continue
			}
			var other ssa.Value
			switch {
			case nilCheckEquivalent(cond.X, v):
				other = cond.Y
			case nilCheckEquivalent(cond.Y, v):
				other = cond.X
			default:
				continue
			}
			equalBlock, notEqualBlock := trueBlock, falseBlock
			if cond.Op == token.NEQ {
				equalBlock, notEqualBlock = falseBlock, trueBlock
			}
			// v != nil establishes non-nil on the not-equal branch. v == sentinel
			// establishes it on the equal branch (a nil sentinel is treated as a
			// deliberate check too; that case is rare and indistinguishable statically).
			nonNilBlock := equalBlock
			if isNilConst(other) {
				nonNilBlock = notEqualBlock
			}
			if establishes(nonNilBlock, block) {
				return true
			}
		}
	}
	return false
}

// establishes reports whether a property that holds on branch is guaranteed to
// hold at block. branch must dominate block, and branch must have a single
// predecessor so it is entered only via its conditional edge (see isNilChecked).
func establishes(branch, block *ssa.BasicBlock) bool {
	return len(branch.Preds) == 1 && branch.Dominates(block)
}

// isErrorsIsOrAs checks if the call is errors.Is(v, target) or errors.As(v, ...)
// in a form that, when true, implies v is non-nil. errors.Is(v, nil) is excluded
// because it is true exactly when v is nil. A *variable* target that happens to
// hold nil has the same property but is not statically distinguishable, so it
// is trusted (mirroring the nil-sentinel tradeoff in isNilChecked).
func isErrorsIsOrAs(call *ssa.Call, v ssa.Value) bool {
	pkg, name, ok := shared.CalleeInfo(call)
	if !ok || pkg != "errors" || (name != "Is" && name != "As") {
		return false
	}
	args := call.Call.Args
	if len(args) == 0 || !nilCheckEquivalent(args[0], v) {
		return false
	}
	// errors.Is(v, nil) is true only when v IS nil. (errors.As's target is a
	// non-nil pointer, so it has no equivalent case.)
	if name == "Is" && len(args) >= 2 && isNilConst(args[1]) {
		return false
	}
	return true
}

func isNilConst(v ssa.Value) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.IsNil()
}
