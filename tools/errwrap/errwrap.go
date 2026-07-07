// NOTE: This analyzer was written largely with the assistance of LLM tooling.
// The SSA dataflow reasoning here is subtle (concurrency soundness, cycle
// handling, go/ssa lowering quirks), so review and test changes with care.

// Package errwrap provides a Go analyzer that ensures all error returns
// are wrapped with errutil.With or errutil.Wrap instead of being returned directly.
package errwrap

import (
	"go/token"
	"go/types"

	"github.com/graxinc/errutil"
	"github.com/graxinc/errutil/tools/internal/shared"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/buildssa"
	"golang.org/x/tools/go/ssa"
)

const (
	directiveUnwrapped = "errutil:unwrapped"
	directiveNew       = "errutil:new"
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
	ssaInfo, err := shared.BuildSSA(pass)
	if err != nil {
		return nil, errutil.With(err)
	}
	unwrappedDirectives := shared.CollectDirectives(pass, directiveUnwrapped)
	newDirectives := shared.CollectDirectives(pass, directiveNew)

	chk := &checker{
		pass:   pass,
		fields: collectFieldWrites(ssaInfo),
		memo:   map[fieldKey]bool{},
		inProg: map[fieldKey]bool{},
	}

	shared.WalkNonGenerated(pass, ssaInfo, func(fn *ssa.Function) {
		chk.checkFunction(fn, unwrappedDirectives, newDirectives)
	})

	shared.ReportUnused(pass, unwrappedDirectives, "unused errutil:unwrapped directive")
	shared.ReportUnused(pass, newDirectives, "unused errutil:new directive")
	return nil, nil
}

// checker holds the per-package state that the wrap checks read. isWrapped has
// no package-wide view on its own; the fields map, precomputed once, lets
// isFieldAlwaysWrapped prove a whole-field invariant without re-walking the
// package per query. memo/inProg memoize that per-field result and break cycles.
type checker struct {
	pass   *analysis.Pass
	fields map[fieldKey]*fieldWrites
	memo   map[fieldKey]bool
	inProg map[fieldKey]bool
}

func (c *checker) checkFunction(fn *ssa.Function, unwrappedDirectives, newDirectives []*shared.Directive) {
	ctx, ok := shared.NewFuncContext(c.pass, fn)
	if !ok {
		return
	}
	errIndices := shared.ErrorResultIndices(fn.Signature)
	// A Baser accessor (`func (T) Base() error`) must return its underlying error
	// RAW — wrapping there is wrong (it stamps a bogus frame on every chain walk
	// and breaks the errutil.Baser contract). Name+signature is exactly Baser, so
	// exempt such methods from the unwrapped rule. The errutil:new rule is unaffected.
	baserAccessor := isBaserAccessor(fn)
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
			if call, ok := instr.(ssa.CallInstruction); ok && call.Pos().IsValid() && shared.WrapsErrorConstructor(call) {
				ctx.ReportRange(newDirectives, call.Pos(), endOf(call.Pos()),
					"use errutil.New instead of wrapping errors.New or fmt.Errorf")
			}

			// Check for unwrapped error returns
			ret, ok := instr.(*ssa.Return)
			if !ok || !ret.Pos().IsValid() {
				continue
			}
			if baserAccessor {
				continue
			}
			for _, errIdx := range errIndices {
				if errIdx >= len(ret.Results) {
					continue
				}
				if !c.isWrapped(ret.Results[errIdx], make(map[ssa.Value]bool)) {
					ctx.ReportRange(unwrappedDirectives, ret.Pos(), endOf(ret.Pos()),
						"error should be wrapped with errutil.With or errutil.Wrap")
				}
			}
		}
	}
}

// isBaserAccessor reports whether fn has exactly the errutil.Baser signature:
// a method named "Base" with no parameters (besides the receiver) and a single
// error result. Keying on name+signature is sufficient; a coincidental non-Baser
// Base() error being exempted from a wrap-nag is harmless.
func isBaserAccessor(fn *ssa.Function) bool {
	sig := fn.Signature
	return fn.Name() == "Base" &&
		sig.Recv() != nil &&
		sig.Params().Len() == 0 &&
		sig.Results().Len() == 1 &&
		shared.IsErrorType(sig.Results().At(0).Type())
}

func (c *checker) isWrapped(v ssa.Value, visited map[ssa.Value]bool) bool {
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
			if !c.isWrapped(edge, visited) {
				return false
			}
		}
		return true
	case *ssa.Extract:
		if call, ok := shared.UnderlyingCall(val); ok {
			return shared.IsErrUtilCall(call)
		}
	case *ssa.MakeInterface:
		return c.isWrapped(val.X, visited)
	case *ssa.ChangeInterface:
		return c.isWrapped(val.X, visited)
	case *ssa.TypeAssert:
		return c.isWrapped(val.X, visited)
	case *ssa.UnOp:
		if alloc, ok := val.X.(*ssa.Alloc); ok {
			return c.isAllocWrapped(alloc, val, visited)
		}
		// A load of a struct field is wrapped when the field is provably
		// always-wrapped across the whole package (see isFieldAlwaysWrapped).
		if fa, ok := val.X.(*ssa.FieldAddr); ok {
			if key, ok := fieldAddrKey(fa); ok && c.isFieldAlwaysWrapped(key) {
				return true
			}
		}
		return c.isWrapped(val.X, visited)
	}
	return false
}

func (c *checker) isAllocWrapped(alloc *ssa.Alloc, load *ssa.UnOp, visited map[ssa.Value]bool) bool {
	if alloc.Referrers() == nil {
		return false
	}
	// A deferred closure that stores into alloc (a captured named result) runs
	// after every return statement, so its stores can override any store made
	// before the return — e.g. the idiomatic `defer func() { err = errutil.With(err) }()`.
	wrapped, overrides, found := c.deferredStoresWrapped(alloc, load.Block(), visited)
	if found {
		if !wrapped {
			return false
		}
		if overrides {
			return true
		}
		// The deferred stores are wrapped but conditional on something other
		// than the value's nil-ness, so the pre-return value can still reach
		// callers on their skip path — it must be wrapped too (checked below).
	}
	// If the load's block contains stores before the load, the last one
	// dominates it, so only its value matters — earlier stores are overwritten,
	// and stores after the load cannot affect it.
	var lastStore *ssa.Store
	for _, instr := range load.Block().Instrs {
		if instr == load {
			break
		}
		if store, ok := instr.(*ssa.Store); ok && store.Addr == alloc {
			lastStore = store
		}
	}
	if lastStore != nil {
		return c.isWrapped(lastStore.Val, visited)
	}
	// Fallback: check all stores across all blocks.
	for _, ref := range *alloc.Referrers() {
		if store, ok := ref.(*ssa.Store); ok && store.Addr == alloc {
			if !c.isWrapped(store.Val, visited) {
				return false
			}
		}
	}
	return true
}

// deferredStoresWrapped inspects deferred closures in alloc's function that
// capture alloc and store to it. found is false when no such store exists.
// wrapped reports whether every such store is wrapped. overrides reports
// whether the deferred stores alone determine the value callers observe:
// every store must run on every path through its closure, or be guarded by a
// nil check on the captured value itself (the idiomatic
// `if err != nil { err = errutil.With(err) }`, whose skip path leaves only a
// nil behind). A store under any other condition cannot vouch for the
// pre-return value on its skip path. Only defers guaranteed to be registered
// before the load (their block dominates the load's block) are considered: a
// conditionally registered defer cannot vouch for every path.
func (c *checker) deferredStoresWrapped(alloc *ssa.Alloc, loadBlock *ssa.BasicBlock, visited map[ssa.Value]bool) (wrapped, overrides, found bool) {
	wrapped, overrides = true, true
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
			closure, ok := mc.Fn.(*ssa.Function)
			if !ok {
				continue
			}
			for i, binding := range mc.Bindings {
				if binding != alloc {
					continue
				}
				fv := closure.FreeVars[i]
				for _, ref := range shared.Referrers(fv) {
					store, ok := ref.(*ssa.Store)
					if !ok {
						continue
					}
					found = true
					wrapped = wrapped && c.isWrapped(store.Val, visited)
					overrides = overrides && storeOverrides(store, fv, closure)
				}
			}
		}
	}
	return wrapped, overrides, found
}

// storeOverrides reports whether a deferred store determines the value callers
// observe regardless of the pre-return value: it runs on every completing path
// through the closure, or it is guarded by a nil check on the captured value
// itself, so the only pre-return value surviving its skip path is nil.
// ponytail: only the immediate `if *fv != nil`-shaped guard on an
// every-path branch is recognized; extend to dominated guards if real code
// needs it.
func storeOverrides(store *ssa.Store, fv *ssa.FreeVar, closure *ssa.Function) bool {
	if dominatesAllReturns(store.Block(), closure) {
		return true
	}
	b := store.Block()
	if len(b.Preds) != 1 {
		return false
	}
	pred := b.Preds[0]
	if !dominatesAllReturns(pred, closure) || len(pred.Instrs) == 0 {
		return false
	}
	ifInstr, ok := pred.Instrs[len(pred.Instrs)-1].(*ssa.If)
	if !ok || pred.Succs[0] == pred.Succs[1] {
		return false
	}
	cmp, ok := ifInstr.Cond.(*ssa.BinOp)
	if !ok || (cmp.Op != token.EQL && cmp.Op != token.NEQ) || !isNilCheckOf(cmp, fv) {
		return false
	}
	nonNil := pred.Succs[0] // NEQ: the true branch is the non-nil one; EQL flips.
	if cmp.Op == token.EQL {
		nonNil = pred.Succs[1]
	}
	return b == nonNil
}

// isNilCheckOf reports whether cmp compares a load of fv against nil.
func isNilCheckOf(cmp *ssa.BinOp, fv *ssa.FreeVar) bool {
	isLoad := func(v ssa.Value) bool {
		un, ok := v.(*ssa.UnOp)
		return ok && un.Op == token.MUL && un.X == fv
	}
	return (isLoad(cmp.X) && shared.IsNilConst(cmp.Y)) || (shared.IsNilConst(cmp.X) && isLoad(cmp.Y))
}

// dominatesAllReturns reports whether b dominates every return block of fn —
// i.e. every completing path through fn passes through b.
func dominatesAllReturns(b *ssa.BasicBlock, fn *ssa.Function) bool {
	for _, rb := range fn.Blocks {
		if len(rb.Instrs) == 0 {
			continue
		}
		if _, ok := rb.Instrs[len(rb.Instrs)-1].(*ssa.Return); ok && !b.Dominates(rb) {
			return false
		}
	}
	return true
}

// fieldKey identifies a struct field by its named struct type and field index.
type fieldKey struct {
	named *types.Named
	field int
}

// fieldWrites records, for one field key, every store to it seen in the package
// and whether the field's address ever escapes past a plain store/load.
type fieldWrites struct {
	stores  []*ssa.Store
	escapes bool
}

// fieldAddrKey returns the (named struct type, field index) key for fa. ok is
// false when fa's struct is not a named type — an unnamed struct can never be
// in scope for the always-wrapped rule, so callers bail (treat as unwrapped).
func fieldAddrKey(fa *ssa.FieldAddr) (fieldKey, bool) {
	ptr, ok := fa.X.Type().(*types.Pointer)
	if !ok {
		return fieldKey{}, false
	}
	named, ok := ptr.Elem().(*types.Named)
	if !ok {
		return fieldKey{}, false
	}
	return fieldKey{named: named, field: fa.Field}, true
}

// collectFieldWrites walks the whole package once, grouping every *ssa.FieldAddr
// by field key. For each it records stores through that address and flags escape
// (any use other than being a store's Addr or a load's operand). This is the
// single package-wide walk isFieldAlwaysWrapped reads from.
func collectFieldWrites(ssaInfo *buildssa.SSA) map[fieldKey]*fieldWrites {
	m := map[fieldKey]*fieldWrites{}
	shared.WalkFunctions(ssaInfo.Pkg, ssaInfo.SrcFuncs, func(fn *ssa.Function) {
		for _, b := range fn.Blocks {
			for _, instr := range b.Instrs {
				fa, ok := instr.(*ssa.FieldAddr)
				if !ok {
					continue
				}
				key, ok := fieldAddrKey(fa)
				if !ok {
					continue
				}
				fw := m[key]
				if fw == nil {
					fw = &fieldWrites{}
					m[key] = fw
				}
				for _, ref := range shared.Referrers(fa) {
					switch r := ref.(type) {
					case *ssa.Store:
						if r.Addr == fa {
							fw.stores = append(fw.stores, r)
						} else {
							fw.escapes = true // fa stored as a value ⇒ address escapes
						}
					case *ssa.UnOp:
						if r.Op != token.MUL || r.X != fa {
							fw.escapes = true
						}
					default:
						fw.escapes = true
					}
				}
			}
		}
	})
	return m
}

// isFieldAlwaysWrapped reports whether every value ever assigned to the field is
// wrapped (or nil). If so, then under the data-race-free assumption any read
// observes some wrapped value regardless of goroutine interleaving, so no
// ordering/adjacency reasoning is needed and there is no concurrency hole.
//
// Soundness rests on guard #1 (SCOPE): an unexported field of a package-local
// type can only be written from this package, so the package's SSA holds EVERY
// write. That is what makes the package-local enumeration in collectFieldWrites
// complete — without it "all writes seen here" would not mean "all writes".
func (c *checker) isFieldAlwaysWrapped(key fieldKey) bool {
	if v, ok := c.memo[key]; ok {
		return v
	}
	// A field whose wrappedness recursively depends on itself: treat the
	// in-progress edge as wrapped so recursion terminates, matching the
	// phi-cycle handling in isWrapped.
	if c.inProg[key] {
		return true
	}
	c.inProg[key] = true
	result := c.computeFieldAlwaysWrapped(key)
	delete(c.inProg, key)
	c.memo[key] = result
	return result
}

func (c *checker) computeFieldAlwaysWrapped(key fieldKey) bool {
	// Guard #1: SCOPE — unexported field of a named struct declared in this package.
	obj := key.named.Obj()
	if obj == nil || obj.Pkg() != c.pass.Pkg {
		return false
	}
	st, ok := key.named.Underlying().(*types.Struct)
	if !ok || key.field >= st.NumFields() || st.Field(key.field).Exported() {
		return false
	}
	fw := c.fields[key]
	// A field with no writes at all is not a wrapped-by-construction field; it is
	// just a zero value being read. Leave such reads flagged (the safe direction —
	// returning false can only ever cause more flagging, never a false negative).
	if fw == nil || len(fw.stores) == 0 {
		return false
	}
	// Guard #2: NO ESCAPE — a write could otherwise occur through an unseen pointer.
	if fw.escapes {
		return false
	}
	// Guard #3: ALL WRITES WRAPPED. go/ssa lowers composite literals (T{err: x})
	// to Alloc+FieldAddr+Store, so this enumeration also covers constructor and
	// literal initialization — essential, since a constructor storing a
	// caller-supplied error means the field is NOT always wrapped.
	for _, store := range fw.stores {
		if !c.isWrapped(store.Val, make(map[ssa.Value]bool)) {
			return false
		}
	}
	return true
}
