// Package errunchecked provides a Go analyzer that detects when errutil.With or errutil.Wrap
// is called directly on a function call result without first checking for nil.
package errunchecked

import (
	"fmt"
	"go/constant"
	"go/token"
	"go/types"
	"slices"

	"github.com/graxinc/errutil"
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
		FactTypes: []analysis.Fact{(*nonNilError)(nil), (*nonNilWhenTrue)(nil), (*nonNilWhenArgNonNil)(nil), (*nonNilVar)(nil)},
		Run:       run,
	}
}

func run(pass *analysis.Pass) (any, error) {
	ssaInfo, ok := pass.ResultOf[buildssa.Analyzer].(*buildssa.SSA)
	if !ok {
		return nil, errutil.New(errutil.Tags{"msg": "unexpected buildssa result type", "type": fmt.Sprintf("%T", pass.ResultOf[buildssa.Analyzer])})
	}
	directives := shared.CollectDirectives(pass, directivePrefix)

	c := &checker{
		pass:           pass,
		ssaPkg:         ssaInfo.Pkg,
		srcFuncs:       ssaInfo.SrcFuncs,
		memo:           map[funcResult]bool{},
		inProgress:     map[funcResult]bool{},
		predMemo:       map[funcResult]bool{},
		predInProgress: map[funcResult]bool{},
		argMemo:        map[funcResult]bool{},
		argInProgress:  map[funcResult]bool{},
	}

	// Export facts for this package's functions so that importing packages can
	// recognize them (cross-package callees have no SSA body to inspect):
	// "always returns non-nil error" and "returns true only when its error
	// parameter is non-nil". This also warms the memos for the check phase
	// below. Generated functions still export facts — they can vouch for
	// handwritten callers — but are not checked below.
	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, c.exportFacts)
	c.exportVarFacts()

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

// checker holds per-package state for the analysis: the pass and its SSA, plus
// memos and in-progress sets for the (potentially recursive) non-nil-return
// and true-implies-non-nil computations, and the lazily built never-nil
// sentinel var verdicts.
type checker struct {
	pass           *analysis.Pass
	ssaPkg         *ssa.Package
	srcFuncs       []*ssa.Function
	memo           map[funcResult]bool
	inProgress     map[funcResult]bool
	predMemo       map[funcResult]bool
	predInProgress map[funcResult]bool
	argMemo        map[funcResult]bool
	argInProgress  map[funcResult]bool

	sentinels         map[*ssa.Global]bool // nil until built by localSentinels
	sentinelsBuilding bool
}

// nonNilError is a fact attached to a function whose error result at each listed
// index is non-nil on every return path. It lets importing packages recognize a
// callee as never-nil even though its body is not available cross-package.
type nonNilError struct {
	Indices []int
}

func (*nonNilError) AFact() {}

func (f *nonNilError) String() string { return fmt.Sprintf("nonNilError%v", f.Indices) }

// nonNilWhenTrue is a fact attached to a bool-returning function that returns
// true only when its error parameter at each listed index is non-nil. It lets
// importing packages treat a dominating `if helper(err)` true branch as a nil
// check on err. Indices are in ssa argument order, so a method's receiver is
// index 0.
type nonNilWhenTrue struct {
	Params []int
}

func (*nonNilWhenTrue) AFact() {}

func (f *nonNilWhenTrue) String() string { return fmt.Sprintf("nonNilWhenTrue%v", f.Params) }

// nonNilWhenArgNonNil is a fact attached to a function with a single error
// result that is non-nil whenever its argument at each listed index is non-nil
// — the nil-preserving-wrapper shape (`func(err error) error { if err == nil
// { return nil }; return wrap(err) }`). It lets importing packages prove a
// call F(x) non-nil when the argument x is itself non-nil at the call site.
// Indices are in ssa argument order, so a method's receiver is index 0.
type nonNilWhenArgNonNil struct {
	Params []int
}

func (*nonNilWhenArgNonNil) AFact() {}

func (f *nonNilWhenArgNonNil) String() string {
	return fmt.Sprintf("nonNilWhenArgNonNil%v", f.Params)
}

// nonNilVar is a fact attached to a package-level error var that is a never-nil
// sentinel: assigned at initialization, every store provably non-nil, and its
// address never escaping its package. An importer may still legally reassign
// an exported var, which the home package cannot see; sentinel reassignment is
// treated as adversarial and ignored.
type nonNilVar struct{}

func (*nonNilVar) AFact() {}

func (*nonNilVar) String() string { return "nonNilVar" }

type funcResult struct {
	fn  *ssa.Function
	idx int
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
			if !ok || !call.Pos().IsValid() || !c.isDirectWrapCall(call) {
				continue
			}
			if callEnds == nil {
				callEnds = shared.SubjectEnds(fn)
			}
			ctx.ReportRange(directives, call.Pos(), callEnds[call.Pos()],
				"do not directly wrap function calls; check for nil first")
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
// guarded by a dominating nil check, or a call to a nil-ness-preserving helper
// whose preserved argument is itself non-nil here. Nil-ness-preserving
// interface conversions are looked through: a proof on any value in the chain
// suffices.
func (c *checker) provedNonNil(v ssa.Value, block *ssa.BasicBlock) bool {
	for ; v != nil; v = conversionSource(v) {
		if c.valueNonNil(v) || c.isNilChecked(v, block, true) || c.callPreservingArgNonNil(v, block) {
			return true
		}
	}
	return false
}

// callPreservingArgNonNil reports whether v is a call F(..., x, ...) where F
// preserves the nil-ness of argument x (carries nonNilWhenArgNonNil for that
// index) and x is itself provably non-nil at block. Unlike callPreservesArg
// (which derives the fact and judges the enclosing function's parameter), this
// proof needs the call site's block context to establish the argument non-nil.
func (c *checker) callPreservingArgNonNil(v ssa.Value, block *ssa.BasicBlock) bool {
	call, ok := v.(*ssa.Call)
	if !ok {
		return false
	}
	callee := call.Call.StaticCallee()
	for i, arg := range call.Call.Args {
		if c.calleeResultNonNilWhenArgNonNil(callee, i) && c.provedNonNil(arg, block) {
			return true
		}
	}
	return false
}

// exportFacts computes and exports facts for fn: nonNilError for error results
// that are always non-nil, and nonNilWhenTrue for bool predicates whose true
// result implies an error parameter is non-nil.
func (c *checker) exportFacts(fn *ssa.Function) {
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

	var params []int
	for i := range fn.Params {
		if c.trueImpliesNonNil(fn, i) {
			params = append(params, i)
		}
	}
	if len(params) > 0 {
		c.pass.ExportObjectFact(obj, &nonNilWhenTrue{Params: params})
	}

	var argParams []int
	for i := range fn.Params {
		if c.resultNonNilWhenArgNonNil(fn, i) {
			argParams = append(argParams, i)
		}
	}
	if len(argParams) > 0 {
		c.pass.ExportObjectFact(obj, &nonNilWhenArgNonNil{Params: argParams})
	}
}

// exportVarFacts exports a nonNilVar fact for each of this package's exported
// never-nil sentinel vars, so importing packages can prove against them.
func (c *checker) exportVarFacts() {
	for g, nonNil := range c.localSentinels() {
		obj := g.Object()
		if !nonNil || obj == nil || obj.Pkg() != c.pass.Pkg || !obj.Exported() {
			continue
		}
		c.pass.ExportObjectFact(obj, &nonNilVar{})
	}
}

// valueNonNil reports whether v is a provably non-nil error: an interface
// construction (which is never nil, even when boxing a nil pointer), a known
// non-nil constructor, a call whose callee always returns a non-nil error, or
// a load of a never-nil sentinel var.
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
	case *ssa.UnOp:
		if val.Op == token.MUL {
			if g, ok := val.X.(*ssa.Global); ok {
				return c.globalNonNil(g)
			}
		}
	}
	return false
}

// globalNonNil reports whether g is a never-nil sentinel var. For this
// package's globals it scans the package; for imported ones it consults a
// nonNilVar fact.
func (c *checker) globalNonNil(g *ssa.Global) bool {
	if g.Pkg == c.ssaPkg {
		return c.localSentinels()[g]
	}
	obj := g.Object()
	if obj == nil {
		return false
	}
	var fact nonNilVar
	return c.pass.ImportObjectFact(obj, &fact)
}

// localSentinels computes never-nil verdicts for this package's error vars,
// once: a single pass classifies every appearance of a global as a load
// (benign), a store (the value must prove non-nil), or anything else (the
// address escapes and stores can no longer be tracked). A sentinel must also
// be stored in the package initializer, or it would be nil before its first
// assignment. While building, re-entrant queries (a store whose value depends
// on another global) resolve to false, conservatively but deterministically.
func (c *checker) localSentinels() map[*ssa.Global]bool {
	if c.sentinels != nil || c.sentinelsBuilding {
		return c.sentinels
	}
	c.sentinelsBuilding = true
	defer func() { c.sentinelsBuilding = false }()

	stores := map[*ssa.Global][]ssa.Value{}
	initStored := map[*ssa.Global]bool{}
	escaped := map[*ssa.Global]bool{}
	initFn := c.ssaPkg.Func("init")
	shared.WalkFunctions(c.ssaPkg, c.srcFuncs, func(fn *ssa.Function) {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				switch in := instr.(type) {
				case *ssa.UnOp:
					if in.Op == token.MUL {
						if _, ok := in.X.(*ssa.Global); ok {
							continue // a plain load
						}
					}
				case *ssa.Store:
					if g, ok := in.Addr.(*ssa.Global); ok {
						stores[g] = append(stores[g], in.Val)
						if fn == initFn {
							initStored[g] = true
						}
						// The stored value may itself be a global's address.
						if vg, ok := in.Val.(*ssa.Global); ok {
							escaped[vg] = true
						}
						continue
					}
				}
				for _, op := range instr.Operands(nil) {
					if g, ok := (*op).(*ssa.Global); ok {
						escaped[g] = true
					}
				}
			}
		}
	})

	verdicts := map[*ssa.Global]bool{}
	for _, mem := range c.ssaPkg.Members {
		g, ok := mem.(*ssa.Global)
		if !ok || !isErrorVar(g) || escaped[g] || !initStored[g] {
			continue
		}
		nonNil := true
		for _, val := range stores[g] {
			if !c.valueNonNil(val) {
				nonNil = false
				break
			}
		}
		verdicts[g] = nonNil
	}
	c.sentinels = verdicts
	return c.sentinels
}

// isErrorVar reports whether g is a package-level error variable (a Global's
// type is a pointer to the var's type).
func isErrorVar(g *ssa.Global) bool {
	ptr, ok := g.Type().(*types.Pointer)
	return ok && shared.IsErrorType(ptr.Elem())
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
	return memoized(c.memo, c.inProgress, funcResult{fn, idx}, func() bool {
		return c.computeResultNonNil(fn, idx)
	})
}

// memoized returns the cached result for key, running compute on a miss.
// Recursion is treated conservatively: a key already being computed yields
// false without caching, so the final verdict is still computed and stored.
func memoized(memo, inProgress map[funcResult]bool, key funcResult, compute func() bool) bool {
	if v, ok := memo[key]; ok {
		return v
	}
	if inProgress[key] {
		return false
	}
	inProgress[key] = true
	res := compute()
	delete(inProgress, key)
	memo[key] = res
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

// trueImpliesNonNil reports whether fn returning true implies its parameter at
// idx (an error) was non-nil — the `isFooErr(err)` predicate-helper shape —
// memoized across the package. Recursion is treated conservatively (a cycle
// yields false), as is a function with no body. idx is in ssa parameter order,
// so a method's receiver is index 0.
func (c *checker) trueImpliesNonNil(fn *ssa.Function, idx int) bool {
	if len(fn.Blocks) == 0 || !isBoolPredicate(fn) ||
		idx >= len(fn.Params) || !shared.IsErrorType(fn.Params[idx].Type()) {
		return false
	}
	return memoized(c.predMemo, c.predInProgress, funcResult{fn, idx}, func() bool {
		return c.computeTrueImpliesNonNil(fn, idx)
	})
}

// computeTrueImpliesNonNil proves the implication per return: a return is safe
// when it is dominated by a check establishing the parameter non-nil (any true
// it returns is covered by the check), or when the returned value itself can
// only be true with the parameter non-nil (valueImpliesNonNil, covering merged
// conditions like `return err != nil && ok`).
func (c *checker) computeTrueImpliesNonNil(fn *ssa.Function, idx int) bool {
	param := fn.Params[idx]
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
		if len(ret.Results) != 1 {
			return false
		}
		if c.isNilChecked(param, b, false) {
			continue
		}
		if !c.valueImpliesNonNil(ret.Results[0], param, true, map[*ssa.Phi]bool{}) {
			return false
		}
	}
	return sawReturn
}

// valueImpliesNonNil reports whether v evaluating to want implies param was
// non-nil — the value-level counterpart of condEstablishesNonNil, for
// predicates whose result is a merged condition rather than a branch to
// distinct returns. A bool constant satisfies any judgment its value cannot
// trigger; a recognized condition shape satisfies its polarity; negation
// flips want; and a phi holds when every incoming value either satisfies the
// judgment or arrives only while param was known non-nil — via a dominating
// check on the predecessor, or via the edge itself when the predecessor
// branches directly into the merge (the short-circuit shape: in
// `return err != nil && ok` the unguarded incoming is constant false, and the
// guarded one arrives from the non-nil branch).
//
// Phi cycles are handled inductively over loop iterations: while a phi's
// obligations are being checked it is assumed to satisfy them (assumed maps
// the phi to its want), so a loop accumulator like `res = res || err != nil`
// discharges its res-was-already-true edge by the induction hypothesis. The
// hypothesis stands only if every base edge proves out; any failure discards
// the whole proof.
func (c *checker) valueImpliesNonNil(v ssa.Value, param ssa.Value, want bool, assumed map[*ssa.Phi]bool) bool {
	if cnst, ok := v.(*ssa.Const); ok {
		return isBoolConst(cnst, !want)
	}
	if onTrue, ok := c.condImpliesNonNil(v, param, false); ok && onTrue == want {
		return true
	}
	// condImpliesNonNil only negates recognized conditions; recursing here
	// also covers negation over phis and constants.
	if un, ok := v.(*ssa.UnOp); ok && un.Op == token.NOT {
		return c.valueImpliesNonNil(un.X, param, !want, assumed)
	}
	phi, ok := v.(*ssa.Phi)
	if !ok {
		return false
	}
	if w, ok := assumed[phi]; ok {
		return w == want // the induction hypothesis; a flipped want proves nothing
	}
	assumed[phi] = want
	defer delete(assumed, phi)
	for i, edge := range phi.Edges {
		pred := phi.Block().Preds[i]
		if c.isNilChecked(param, pred, false) || c.edgeEstablishes(pred, phi.Block(), param, assumed) {
			continue
		}
		if !c.valueImpliesNonNil(edge, param, want, assumed) {
			return false
		}
	}
	return true
}

// edgeEstablishes reports whether traversing the CFG edge pred→succ implies
// param was non-nil: pred must end in an If with exactly one successor equal
// to succ, fixing the condition's truth value on the edge, and that truth
// value must imply non-nil — directly, or through the induction hypothesis of
// an assumed phi. This is the φ-incoming-edge counterpart of establishes,
// which needs a dominated block and cannot see facts that exist only on the
// edge into a merge (the short-circuit and loop-accumulator shapes).
func (c *checker) edgeEstablishes(pred, succ *ssa.BasicBlock, param ssa.Value, assumed map[*ssa.Phi]bool) bool {
	if len(pred.Instrs) == 0 {
		return false
	}
	ifInstr, ok := pred.Instrs[len(pred.Instrs)-1].(*ssa.If)
	if !ok || pred.Succs[0] == pred.Succs[1] {
		return false
	}
	var condIs bool // the condition's value on this edge
	switch succ {
	case pred.Succs[0]:
		condIs = true
	case pred.Succs[1]:
		condIs = false
	default:
		return false
	}
	cond := ifInstr.Cond
	for {
		un, ok := cond.(*ssa.UnOp)
		if !ok || un.Op != token.NOT {
			break
		}
		cond, condIs = un.X, !condIs
	}
	if onTrue, ok := c.condImpliesNonNil(cond, param, false); ok && onTrue == condIs {
		return true
	}
	if phi, ok := cond.(*ssa.Phi); ok {
		if w, ok := assumed[phi]; ok && w == condIs {
			return true
		}
	}
	return false
}

// calleeTrueImpliesNonNil reports whether callee returning true implies its
// parameter at idx was non-nil. For callees with a body (this package) it
// inspects the returns; for callees without one (another package) it consults
// an imported fact.
func (c *checker) calleeTrueImpliesNonNil(callee *ssa.Function, idx int) bool {
	if callee == nil {
		return false
	}
	if len(callee.Blocks) > 0 {
		return c.trueImpliesNonNil(callee, idx)
	}
	obj := callee.Object()
	if obj == nil {
		return false
	}
	var fact nonNilWhenTrue
	return c.pass.ImportObjectFact(obj, &fact) && slices.Contains(fact.Params, idx)
}

// resultNonNilWhenArgNonNil reports whether fn's sole error result is non-nil
// on every return whenever its argument at idx (itself an error) is non-nil —
// the nil-preserving-wrapper shape — memoized across the package. Recursion is
// treated conservatively (a cycle yields false), as is a function with no body
// or one whose single result is not an error. idx is in ssa argument order, so
// a method's receiver is index 0.
func (c *checker) resultNonNilWhenArgNonNil(fn *ssa.Function, idx int) bool {
	results := fn.Signature.Results()
	if len(fn.Blocks) == 0 || results.Len() != 1 || !shared.IsErrorType(results.At(0).Type()) ||
		idx >= len(fn.Params) || !shared.IsErrorType(fn.Params[idx].Type()) {
		return false
	}
	return memoized(c.argMemo, c.argInProgress, funcResult{fn, idx}, func() bool {
		return c.computeResultNonNilWhenArgNonNil(fn, idx)
	})
}

// computeResultNonNilWhenArgNonNil proves the implication per return: each
// returned value must be non-nil whenever the parameter at idx is (see
// returnPreservesArg). A return that returns more or fewer than one value (only
// possible for a malformed result count) defeats the proof.
func (c *checker) computeResultNonNilWhenArgNonNil(fn *ssa.Function, idx int) bool {
	param := fn.Params[idx]
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
		if len(ret.Results) != 1 || !c.returnPreservesArg(ret.Results[0], param, b) {
			return false
		}
	}
	return sawReturn
}

// returnPreservesArg reports whether a value returned in block is non-nil
// whenever param is non-nil. It discharges, in order: the value is provably
// non-nil outright; the return is dominated by a `param == nil` branch, so param
// is nil here and the premise is vacuously false; the value is param itself; or
// the value is G(..., param, ...) where G in turn preserves param's nil-ness at
// that argument (transitivity). Nil-ness-preserving conversions on the returned
// value are looked through, as elsewhere.
func (c *checker) returnPreservesArg(v ssa.Value, param ssa.Value, block *ssa.BasicBlock) bool {
	if c.valueNonNil(v) || isNilEstablished(param, block) {
		return true
	}
	for w := v; w != nil; w = conversionSource(w) {
		if nilCheckEquivalent(w, param) || c.callPreservesArg(w, param) {
			return true
		}
	}
	return false
}

// callPreservesArg reports whether v is a call G(..., param, ...) that passes
// param (its nil-ness intact through conversions) at an argument index for
// which G carries a nonNilWhenArgNonNil judgment — proved from G's body for
// same-package callees, imported as a fact cross-package. This is the
// transitive step: G preserves param, so G(param) is non-nil whenever param is.
func (c *checker) callPreservesArg(v ssa.Value, param ssa.Value) bool {
	call, ok := v.(*ssa.Call)
	if !ok {
		return false
	}
	callee := call.Call.StaticCallee()
	for i, arg := range call.Call.Args {
		for a := arg; a != nil; a = conversionSource(a) {
			if nilCheckEquivalent(a, param) && c.calleeResultNonNilWhenArgNonNil(callee, i) {
				return true
			}
		}
	}
	return false
}

// calleeResultNonNilWhenArgNonNil reports whether callee's sole error result is
// non-nil whenever its argument at idx is non-nil. For callees with a body
// (this package) it inspects the returns; for callees without one (another
// package) it consults an imported fact.
func (c *checker) calleeResultNonNilWhenArgNonNil(callee *ssa.Function, idx int) bool {
	if callee == nil {
		return false
	}
	if len(callee.Blocks) > 0 {
		return c.resultNonNilWhenArgNonNil(callee, idx)
	}
	obj := callee.Object()
	if obj == nil {
		return false
	}
	var fact nonNilWhenArgNonNil
	return c.pass.ImportObjectFact(obj, &fact) && slices.Contains(fact.Params, idx)
}

// isBoolPredicate reports whether fn has exactly one result, of boolean type.
func isBoolPredicate(fn *ssa.Function) bool {
	results := fn.Signature.Results()
	if results.Len() != 1 {
		return false
	}
	basic, ok := results.At(0).Type().Underlying().(*types.Basic)
	return ok && basic.Info()&types.IsBoolean != 0
}

func isBoolConst(v ssa.Value, b bool) bool {
	c, ok := v.(*ssa.Const)
	return ok && c.Value != nil && c.Value.Kind() == constant.Bool && constant.BoolVal(c.Value) == b
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
// equality against a sentinel, errors.Is/As/AsType on v, a predicate helper
// whose true result implies v is non-nil, or a comma-ok type assertion on v)
// guards block via establishes. trustSentinels is described on condImpliesNonNil.
func (c *checker) isNilChecked(v ssa.Value, block *ssa.BasicBlock, trustSentinels bool) bool {
	for _, b := range block.Parent().Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		if c.condEstablishesNonNil(ifInstr.Cond, v, b.Succs[0], b.Succs[1], block, trustSentinels) {
			return true
		}
	}
	return false
}

// condEstablishesNonNil reports whether the branch condition cond, with the
// given true/false successors, establishes that v is non-nil at block.
func (c *checker) condEstablishesNonNil(cond, v ssa.Value, trueBlock, falseBlock, block *ssa.BasicBlock, trustSentinels bool) bool {
	onTrue, ok := c.condImpliesNonNil(cond, v, trustSentinels)
	if !ok {
		return false
	}
	branch := trueBlock
	if !onTrue {
		branch = falseBlock
	}
	return establishes(branch, block)
}

// isNilEstablished is the nil-establishing counterpart of isNilChecked: it
// reports whether v is guaranteed nil at block, because a dominating literal
// `v == nil` / `v != nil` branch routes only its nil case here. It backs the
// vacuous arm of the nil-preserving proof (a return reached only when the
// argument is nil imposes no obligation).
func isNilEstablished(v ssa.Value, block *ssa.BasicBlock) bool {
	for _, b := range block.Parent().Blocks {
		if len(b.Instrs) == 0 {
			continue
		}
		ifInstr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
		if !ok {
			continue
		}
		if condEstablishesNil(ifInstr.Cond, v, b.Succs[0], b.Succs[1], block) {
			return true
		}
	}
	return false
}

// condEstablishesNil reports whether the branch condition cond, with the given
// true/false successors, establishes that v is nil at block.
func condEstablishesNil(cond, v ssa.Value, trueBlock, falseBlock, block *ssa.BasicBlock) bool {
	onTrue, ok := condImpliesNil(cond, v)
	if !ok {
		return false
	}
	branch := trueBlock
	if !onTrue {
		branch = falseBlock
	}
	return establishes(branch, block)
}

// condImpliesNonNil reports whether cond is a recognized nil-ness condition on
// v, and if so which truth value of cond implies v is non-nil: onTrue is true
// for conditions like v != nil whose true result implies it, false for ones
// like v == nil whose false result does. trustSentinels admits comparands and
// errors.Is targets that are not provably non-nil as deliberate sentinel
// checks; that reading is only justified in branch position (an `if` written
// against a sentinel), never when deriving facts from returned values.
func (c *checker) condImpliesNonNil(cond, v ssa.Value, trustSentinels bool) (onTrue, ok bool) {
	switch cond := cond.(type) {
	case *ssa.Call:
		if isErrorsIsOrAs(cond, v) {
			// errors.As and errors.AsType are false outright for a nil error,
			// so only errors.Is needs target scrutiny: Is(nil, nil) is true.
			target, isIs := errorsIsTarget(cond)
			if isIs && !c.sentinelTrusted(target, trustSentinels) {
				return false, false
			}
			return true, true
		}
		if c.isNonNilPredicateCall(cond, v) {
			return true, true
		}
	case *ssa.Extract:
		// The ok of a comma-ok type assertion on v: a nil interface never
		// asserts successfully to any type, so ok being true implies v is
		// non-nil. This also covers the if-chain a type switch lowers to
		// (its `case nil` arm lowers to a nil comparison, handled below).
		ta, isAssert := cond.Tuple.(*ssa.TypeAssert)
		if isAssert && cond.Index == 1 && ta.CommaOk && nilCheckEquivalent(ta.X, v) {
			return true, true
		}
		// The ok of errors.AsType[T](v): the generic equivalent of
		// errors.As above — a nil error matches no target type, so ok
		// being true implies v is non-nil.
		if call, isCall := cond.Tuple.(*ssa.Call); isCall && cond.Index == 1 && isErrorsAsType(call, v) {
			return true, true
		}
	case *ssa.UnOp:
		if cond.Op == token.NOT {
			onTrue, ok = c.condImpliesNonNil(cond.X, v, trustSentinels)
			return !onTrue, ok
		}
	case *ssa.BinOp:
		return c.compareImpliesNonNil(cond, v, trustSentinels)
	}
	return false, false
}

// compareImpliesNonNil is the comparison case of condImpliesNonNil: an
// equality or inequality of v against nil or a sentinel.
func (c *checker) compareImpliesNonNil(cond *ssa.BinOp, v ssa.Value, trustSentinels bool) (onTrue, ok bool) {
	other, ok := comparedAgainst(cond, v)
	if !ok {
		return false, false
	}
	otherIsNil := isNilConst(other)
	if !otherIsNil && !c.sentinelTrusted(other, trustSentinels) {
		return false, false
	}
	// v == sentinel implies non-nil when true; v == nil implies it when
	// false. NEQ flips both.
	onTrue = !otherIsNil
	if cond.Op == token.NEQ {
		onTrue = !onTrue
	}
	return onTrue, true
}

// condImpliesNil is the nil-establishing counterpart of condImpliesNonNil: it
// reports whether cond is a literal `v == nil` / `v != nil` comparison (or a
// negation of one), and if so which truth value of cond implies v IS nil. Only
// genuine nil comparisons qualify — unlike the non-nil direction, a sentinel or
// errors.Is condition's complement does not imply v is nil (v may be a
// different non-nil error), so those are deliberately not recognized here.
func condImpliesNil(cond, v ssa.Value) (onTrue, ok bool) {
	switch cond := cond.(type) {
	case *ssa.UnOp:
		if cond.Op == token.NOT {
			onTrue, ok = condImpliesNil(cond.X, v)
			return !onTrue, ok
		}
	case *ssa.BinOp:
		other, ok := comparedAgainst(cond, v)
		if !ok || !isNilConst(other) {
			return false, false
		}
		// v == nil is true exactly when v is nil; != flips.
		return cond.Op == token.EQL, true
	}
	return false, false
}

// comparedAgainst returns the operand an EQL/NEQ comparison binds against v —
// the side that is not v — reporting ok=false if cond is not such a comparison.
func comparedAgainst(cond *ssa.BinOp, v ssa.Value) (other ssa.Value, ok bool) {
	if cond.Op != token.EQL && cond.Op != token.NEQ {
		return nil, false
	}
	switch {
	case nilCheckEquivalent(cond.X, v):
		return cond.Y, true
	case nilCheckEquivalent(cond.Y, v):
		return cond.X, true
	}
	return nil, false
}

// sentinelTrusted reports whether x can stand as the non-nil side of a
// sentinel check: trusted outright in branch position (an `if` deliberately
// written against a sentinel), otherwise it must be provably non-nil — a
// constructed value, a never-nil sentinel var, or a callee with a fact — not,
// say, a forwarded parameter of the enclosing predicate. Trusting unproven
// values outside branch position would, for example, derive a predicate fact
// from the `return err == target` inside errors.Is itself, which is true for
// a nil err and nil target.
func (c *checker) sentinelTrusted(x ssa.Value, trustSentinels bool) bool {
	return trustSentinels || c.valueNonNil(x)
}

// establishes reports whether a property that holds on branch is guaranteed to
// hold at block. branch must dominate block, and branch must have a single
// predecessor so it is entered only via its conditional edge (see isNilChecked).
func establishes(branch, block *ssa.BasicBlock) bool {
	return len(branch.Preds) == 1 && branch.Dominates(block)
}

// isNonNilPredicateCall reports whether call is a bool predicate — the
// `isFooErr(err)` helper shape — whose true result implies v, passed as one of
// its arguments, is non-nil. Static calls only: the arguments align with the
// callee's parameters (a method's receiver is argument 0 of both).
func (c *checker) isNonNilPredicateCall(call *ssa.Call, v ssa.Value) bool {
	callee := call.Call.StaticCallee()
	for i, arg := range call.Call.Args {
		if nilCheckEquivalent(arg, v) && c.calleeTrueImpliesNonNil(callee, i) {
			return true
		}
	}
	return false
}

// errorsIsTarget returns the target argument of an errors.Is call; ok is false
// for any other call.
func errorsIsTarget(call *ssa.Call) (target ssa.Value, ok bool) {
	pkg, name, ok := shared.CalleeInfo(call)
	if !ok || pkg != "errors" || name != "Is" {
		return nil, false
	}
	args := call.Call.Args
	if len(args) < 2 {
		return nil, false
	}
	return args[1], true
}

// isErrorsAsType checks if the call is errors.AsType[T](v). Like errors.As, a
// true ok result implies v is non-nil, since a nil error matches no target
// type. The callee resolves through Origin because the builder may present
// the call as a generic instantiation.
func isErrorsAsType(call *ssa.Call, v ssa.Value) bool {
	callee := call.Call.StaticCallee()
	if callee == nil {
		return false
	}
	if origin := callee.Origin(); origin != nil {
		callee = origin
	}
	if callee.Pkg == nil || callee.Pkg.Pkg.Path() != "errors" || callee.Name() != "AsType" {
		return false
	}
	args := call.Call.Args
	return len(args) > 0 && nilCheckEquivalent(args[0], v)
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
