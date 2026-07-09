package a

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/graxinc/errutil"
)

func returnsErr() error { return nil }

// Bad: direct call without nil check
func badWith() error {
	return errutil.With(returnsErr()) // want `do not directly wrap`
}

func badWrap() error {
	return errutil.Wrap(returnsErr()) // want `do not directly wrap`
}

func badWitht() error {
	return errutil.Witht(returnsErr(), errutil.Tags{}) // want `do not directly wrap`
}

func badWrapt() error {
	return errutil.Wrapt(returnsErr(), errutil.Tags{}) // want `do not directly wrap`
}

// Good: nil checked first
func goodNilChecked() error {
	err := returnsErr()
	if err != nil {
		return errutil.With(err)
	}
	return nil
}

// Good: wrapping a variable
func goodWrapVariable(err error) error {
	return errutil.With(err)
}

var sentinel = errors.New("sentinel")

// Good: errors.Is check guarantees non-nil
func goodErrorsIsCheck() error {
	err := returnsErr()
	if errors.Is(err, sentinel) {
		return errutil.With(err)
	}
	return nil
}

// Good: errors.As check guarantees non-nil
func goodErrorsAsCheck() error {
	err := returnsErr()
	var target *customErr
	if errors.As(err, &target) {
		return errutil.With(err)
	}
	return nil
}

type customErr struct{}

func (customErr) Error() string { return "" }

// Bad: diamond merge — err may be nil on the else path, so the wrap after the
// merge is not guaranteed non-nil and must still be flagged.
func badDiamondMerge() error {
	err := returnsErr()
	var msg string
	if err != nil {
		msg = "bad"
	} else {
		msg = "ok"
	}
	_ = msg
	return errutil.With(err) // want `do not directly wrap`
}

// Good: guard clause (early return on nil) several blocks above the wrap.
func goodGuardClause() error {
	err := returnsErr()
	if err == nil {
		return nil
	}
	for i := 0; i < 1; i++ {
		_ = i
	}
	return errutil.With(err)
}

// Good: wrapping nil
func goodWrapNil() error {
	return errutil.With(nil)
}

// Good: directive on same line
func goodDirectiveSameLine() error {
	return errutil.With(returnsErr()) //errutil:unchecked
}

// Good: directive on line above
func goodDirectiveAbove() error {
	//errutil:unchecked
	return errutil.With(returnsErr())
}

// Good: directive on function
//errutil:unchecked
func goodDirectiveOnFunc() error {
	return errutil.With(returnsErr())
}

// Bad: an error extracted from a multi-result call is a direct call result too.
func returnsTwoValues() (int, error) { return 0, nil }

func badWrapMultiReturnExtract() error {
	_, err := returnsTwoValues()
	return errutil.With(err) // want `do not directly wrap`
}

// Bad: functions without error results are still checked.
func badNoErrorReturn() {
	_ = errutil.With(returnsErr()) // want `do not directly wrap`
}

// Unused directive
func badUnusedDirective(err error) error {
	//errutil:unchecked // want `unused errutil:unchecked directive`
	return errutil.With(err)
}

// A near-miss directive name is not a real directive, so it must not suppress.
func badNearMissDirective() error {
	return errutil.With(returnsErr()) //errutil:uncheckedtypo // want `do not directly wrap`
}

// Bad: `||` short-circuit. The branch is also reachable when `other` is true and
// err is nil, so err is not guaranteed non-nil at the wrap.
func badOrShortCircuit(other bool) error {
	err := returnsErr()
	if err != nil || other {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// Good: `&&` short-circuit. err != nil genuinely dominates the wrap.
func goodAndShortCircuit(other bool) error {
	err := returnsErr()
	if err != nil && other {
		return errutil.With(err)
	}
	return nil
}

// Good: non-nil established on the else branch via err == nil.
func goodElseBranch() error {
	err := returnsErr()
	if err == nil {
		return nil
	} else {
		return errutil.With(err)
	}
}

// Good: equality against a sentinel establishes that err was examined; on the
// equal branch err is that (non-nil) sentinel.
func goodSentinelEqual() error {
	err := returnsErr()
	if err == io.EOF {
		return errutil.With(err)
	}
	return nil
}

// Good: inequality against a sentinel; the non-equal branch returns early, so the
// wrap is reached only when err == io.EOF.
func goodSentinelNotEqualGuard() error {
	err := returnsErr()
	if err != io.EOF {
		return nil
	}
	return errutil.With(err)
}

// Bad: errors.Is(err, nil) is true only when err is nil, so the wrap wraps nil.
func badErrorsIsNil() error {
	err := returnsErr()
	if errors.Is(err, nil) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// Nested wrap: errutil.Wrap never returns nil, so only the inner wrap of the raw
// function result is flagged; the outer wrap needs no nil check.
func badNestedWrapInnerOnly() error {
	return errutil.With(errutil.Wrap(returnsErr())) // want `do not directly wrap`
}

// Good: wrapping a constructor result (errors.New/fmt.Errorf never return nil).
func goodWrapConstructor() error {
	return errutil.With(errors.New("boom"))
}

// Bad: err is checked, then reassigned to a fresh unchecked call before the wrap.
func badReassignAfterCheck() error {
	err := returnsErr()
	if err != nil {
		_ = err
	}
	err = returnsErr()
	return errutil.With(err) // want `do not directly wrap`
}

// Bad: interface method results are function-call results too.
type doer interface{ do() error }

func badIfaceMethod(d doer) error {
	return errutil.With(d.do()) // want `do not directly wrap`
}

// Out of scope: a value merged from multiple paths is an SSA phi, not a direct
// function-call result, so it is not flagged even though it may be nil here.
func goodPhiMergeOutOfScope(cond bool) error {
	var err error
	if cond {
		err = returnsErr()
	}
	return errutil.With(err)
}

// Good: the two-tag variants are checked the same way as With/Wrap.
func goodWitht() error {
	err := returnsErr()
	if err != nil {
		return errutil.Witht(err, errutil.Tags{})
	}
	return nil
}

func goodWrapt() error {
	err := returnsErr()
	if err != nil {
		return errutil.Wrapt(err, errutil.Tags{})
	}
	return nil
}

// Good: ctx.Err() inside a <-ctx.Done() select case is non-nil.
func goodCtxErrDoneSelect(ctx context.Context, c <-chan int) error {
	select {
	case <-ctx.Done():
		return errutil.With(ctx.Err())
	case <-c:
		return nil
	}
}

// Good: ctx.Err() after a bare <-ctx.Done() receive.
func goodCtxErrDoneRecv(ctx context.Context) error {
	<-ctx.Done()
	return errutil.With(ctx.Err())
}

// Bad: ctx.Err() with no Done receive guarding it may be nil.
func badCtxErrNoDone(ctx context.Context) error {
	return errutil.With(ctx.Err()) // want `do not directly wrap`
}

// Bad: ctx.Err() wrapped in a select arm other than the <-ctx.Done() arm. The
// select block dominates every arm, but the context is not done in this arm.
func badCtxErrWrongSelectArm(ctx context.Context, c <-chan int) error {
	select {
	case <-ctx.Done():
		return nil
	case <-c:
		return errutil.With(ctx.Err()) // want `do not directly wrap`
	}
}

// Bad: the Done receive is on a different context than the one being wrapped.
func badCtxErrDifferentContext(ctx1, ctx2 context.Context) error {
	<-ctx1.Done()
	return errutil.With(ctx2.Err()) // want `do not directly wrap`
}

// Bad: the Done receive does not dominate the wrap (it is in a sibling branch).
func badCtxErrDoneDoesNotDominate(ctx context.Context, cond bool) error {
	if cond {
		<-ctx.Done()
	}
	return errutil.With(ctx.Err()) // want `do not directly wrap`
}

// alwaysErr returns a non-nil error on every path.
func alwaysErr() error {
	return errutil.New(nil)
}

// Good: wrapping a function whose every return is non-nil.
func goodWrapAlwaysErr() error {
	return errutil.With(alwaysErr())
}

type boxErr struct{}

func (boxErr) Error() string { return "" }

// boxedErr returns a boxed concrete error on every path (interface values are
// never nil, so this is always non-nil).
func boxedErr(b bool) error {
	if b {
		return boxErr{}
	}
	return &boxErr{}
}

// Good: wrapping a builder that boxes a concrete error on every path.
func goodWrapBoxedErr() error {
	return errutil.With(boxedErr(true))
}

// maybeErr can return nil.
func maybeErr(b bool) error {
	if b {
		return errutil.New(nil)
	}
	return nil
}

// Bad: wrapping a function that can return nil.
func badWrapMaybeErr() error {
	return errutil.With(maybeErr(true)) // want `do not directly wrap`
}

// nilGuardedErr returns non-nil on every path even though the inner call is
// not provably non-nil: the unprovable result is returned only under a
// dominating nil check, and the fallback is a constructor. This is the
// boundary-helper shape for wrapping third-party constructors that route
// through replaceable function variables.
func nilGuardedErr(b bool) error {
	if err := maybeErr(b); err != nil {
		return err
	}
	return errutil.New(nil)
}

// Good: wrapping a nil-guard-proved helper needs no nil check at the call site.
func goodWrapNilGuardedErr() error {
	return errutil.With(nilGuardedErr(true))
}

// partialGuard guards one return but not the other.
func partialGuard(b bool) error {
	if err := maybeErr(b); err != nil {
		return err
	}
	return maybeErr(!b)
}

// Bad: a single unguarded return defeats the proof.
func badWrapPartialGuard() error {
	return errutil.With(partialGuard(true)) // want `do not directly wrap`
}

// maybeErrIface can return a nil defined-interface error.
func maybeErrIface(b bool) errIface {
	if b {
		return customErr{}
	}
	return nil
}

// nilGuardedIfaceErr returns a converted value: the nil check is on the
// pre-conversion errIface value, and the proof looks through the conversion.
func nilGuardedIfaceErr(b bool) error {
	if err := maybeErrIface(b); err != nil {
		return err
	}
	return errutil.New(nil)
}

// Good: the nil-guard proof composes with interface conversions.
func goodWrapNilGuardedIface() error {
	return errutil.With(nilGuardedIfaceErr(true))
}

// forwardsErr forwards a non-nil call's result directly (a tail call). SSA
// expands `return alwaysErr()` into an extract, which valueNonNil recognizes.
func forwardsErr() error {
	return alwaysErr()
}

// Good: wrapping a function whose only return forwards a non-nil result.
func goodWrapForwardsErr() error {
	return errutil.With(forwardsErr())
}

// Bad: direct wrap inside a closure is still flagged (WalkFunctions descends
// into anonymous functions).
func badWrapInClosure() func() error {
	return func() error {
		return errutil.With(returnsErr()) // want `do not directly wrap`
	}
}

// Good: a directive suppresses a wrap inside a closure.
func goodDirectiveInClosure() func() error {
	return func() error {
		return errutil.With(returnsErr()) //errutil:unchecked
	}
}

// concreteErr returns a concrete pointer type, not the error interface.
func concreteErr() *customErr { return nil }

// Good (by construction): wrapping a concrete-typed result is never flagged.
// The argument is an interface boxing (MakeInterface), not a bare call result,
// so it falls outside what the analyzer inspects — and a boxed pointer, even a
// nil one, is a non-nil error interface, so the wrap cannot wrap a nil error.
func goodWrapConcreteTypedNil() error {
	return errutil.With(concreteErr())
}

// recursiveAlwaysErr always returns a non-nil error, but recurses. Cycles are
// handled conservatively, so its result is treated as possibly-nil.
func recursiveAlwaysErr(n int) error {
	if n > 0 {
		return recursiveAlwaysErr(n - 1)
	}
	return errutil.New(nil)
}

// Bad (conservative limitation): wrapping a self-recursive function's result is
// flagged even though every path returns non-nil, because cycle detection yields
// "possibly nil". Codified so a change to recursion handling is a deliberate one.
func badWrapRecursive() error {
	return errutil.With(recursiveAlwaysErr(3)) // want `do not directly wrap`
}

// mutualErrA and mutualErrB always return non-nil but form a two-function cycle.
func mutualErrA(n int) error {
	if n > 0 {
		return mutualErrB(n - 1)
	}
	return errutil.New(nil)
}

func mutualErrB(n int) error {
	if n > 0 {
		return mutualErrA(n - 1)
	}
	return errutil.New(nil)
}

// Bad (conservative limitation): mutual recursion is treated like self-recursion;
// the cycle yields "possibly nil".
func badWrapMutualRecursive() error {
	return errutil.With(mutualErrA(3)) // want `do not directly wrap`
}

// Good: switch err { case nil: ... default: ... } lowers to the same nil
// comparison as an if, so the default arm is known non-nil.
func goodSwitchNilDefault() error {
	err := returnsErr()
	switch err {
	case nil:
		return nil
	default:
		return errutil.With(err)
	}
}

// Good: switch err { case sentinel: ... } — on the matching arm err is that
// (non-nil) sentinel, same as the if-based sentinel checks above.
func goodSwitchSentinel() error {
	err := returnsErr()
	switch err {
	case io.EOF:
		return errutil.With(err)
	}
	return nil
}

// Good: a negated errors.Is guard. The SSA builder compiles !cond by swapping
// the branch targets rather than emitting a NOT, so the fallthrough is the
// errors.Is-true path; codified because the suppression relies on that builder
// behavior.
func goodNegatedErrorsIs() error {
	err := returnsErr()
	if !errors.Is(err, sentinel) {
		return nil
	}
	return errutil.With(err)
}

// Good: a type switch arm for a specific type implies err is non-nil there (a
// nil interface never matches any type case).
func goodTypeSwitch() error {
	err := returnsErr()
	switch err.(type) {
	case *customErr:
		return errutil.With(err)
	}
	return nil
}

// Bad: the nil arm of a type switch is exactly the err == nil case.
func badTypeSwitchNilCase() error {
	err := returnsErr()
	switch err.(type) {
	case nil:
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// Bad (conservative limitation): a multi-type case body is reachable from two
// dispatch branches, so the single-predecessor requirement of establishes fails
// even though err is non-nil on both.
func badTypeSwitchMultiType() error {
	err := returnsErr()
	switch err.(type) {
	case *customErr, boxErr:
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// Good: a comma-ok type assertion guard implies err is non-nil when ok.
func goodCommaOkAssertGuard() error {
	err := returnsErr()
	if _, ok := err.(*customErr); ok {
		return errutil.With(err)
	}
	return nil
}

// Good (by construction): a single-result type assertion panics on a nil
// interface, so the wrap can only be reached with a non-nil value.
func goodWrapTypeAssert(v any) error {
	return errutil.With(v.(error))
}

// Out of scope: the value of a comma-ok assertion is an extract of the assert
// tuple, not a function-call result, so it is not flagged even though it is nil
// whenever ok is false.
func goodCommaOkValueOutOfScope(v any) error {
	e, _ := v.(error)
	return errutil.With(e)
}

// Out of scope: a variable captured by a closure lives on the heap, so reading
// it is a load rather than a direct call result — like the phi case above, it
// is not flagged even though it may be nil here.
func goodCapturedOutOfScope() error {
	err := returnsErr()
	f := func() { _ = err }
	f()
	return errutil.With(err)
}

// Limitation: a nil-valued *variable* target makes errors.Is true exactly when
// err is nil, but variable nil-ness is not statically distinguishable, so the
// check is trusted (only a constant nil target is rejected, see badErrorsIsNil).
var nilTarget error

func goodErrorsIsNilVarLimitation() error {
	err := returnsErr()
	if errors.Is(err, nilTarget) {
		return errutil.With(err)
	}
	return nil
}

// Bad: errors.Join returns nil when all of its arguments are nil, so it is not
// a non-nil constructor and must stay flagged.
func badWrapErrorsJoin(e1, e2 error) error {
	return errutil.With(errors.Join(e1, e2)) // want `do not directly wrap`
}

// Bad: defer and go statements carry the same unchecked wrap.
func badDeferWrap() {
	defer errutil.With(returnsErr()) // want `do not directly wrap`
}

func badGoWrap() {
	go errutil.With(returnsErr()) // want `do not directly wrap`
}

// Good: a nil-checked deferred wrap.
func goodDeferWrapChecked() {
	err := returnsErr()
	if err != nil {
		defer errutil.With(err)
	}
}

// errIface is a defined interface embedding error: converting it to error
// preserves nil-ness (unlike boxing a concrete type, which is never nil).
// Its method set is identical to error's, so the conversion is a ChangeType.
type errIface interface{ error }

func returnsErrIface() errIface { return nil }

// Bad: the conversion to error can carry a nil errIface through.
func badWrapDefinedIface() error {
	return errutil.With(returnsErrIface()) // want `do not directly wrap`
}

// richErrIface has an extra method, so its conversion to error is a
// ChangeInterface rather than a ChangeType; both must be looked through.
type richErrIface interface {
	error
	Code() int
}

func returnsRichErrIface() richErrIface { return nil }

// Bad: same nil carry-through via the ChangeInterface shape.
func badWrapRichIface() error {
	return errutil.With(returnsRichErrIface()) // want `do not directly wrap`
}

// Good: checked via the ChangeInterface shape.
func goodRichIfaceChecked() error {
	e := returnsRichErrIface()
	if e != nil {
		return errutil.With(e)
	}
	return nil
}

// Good: nil check on the pre-conversion value.
func goodDefinedIfaceCheckedInner() error {
	e := returnsErrIface()
	if e != nil {
		return errutil.With(e)
	}
	return nil
}

// Good: nil check on the converted value.
func goodDefinedIfaceCheckedConverted() error {
	var e error = returnsErrIface()
	if e != nil {
		return errutil.With(e)
	}
	return nil
}

func alwaysErrIface() errIface { return customErr{} }

// Good: non-nil-ness is proved through the interface conversion.
func goodWrapAlwaysErrIface() error {
	return errutil.With(alwaysErrIface())
}

// namedAlwaysErr uses a named result and a naked return; SSA register-lifting
// resolves the return to the constructor call, so it is recognized as non-nil.
func namedAlwaysErr() (err error) {
	err = errutil.New(nil)
	return
}

func goodWrapNamedAlwaysErr() error {
	return errutil.With(namedAlwaysErr())
}

// genericAlwaysErr proves non-nil-ness works through a generic instantiation.
func genericAlwaysErr[T any]() error {
	return errutil.New(nil)
}

func goodWrapGenericAlwaysErr() error {
	return errutil.With(genericAlwaysErr[int]())
}

// Methods are proved from their bodies the same way as functions.
type alwaysSvc struct{}

func (alwaysSvc) err() error { return errutil.New(nil) }

func goodWrapMethodAlwaysErr() error {
	var s alwaysSvc
	return errutil.With(s.err())
}

// Good: ctx.Err() inside a for-select loop's Done arm.
func goodCtxErrForSelect(ctx context.Context, c <-chan int) error {
	for {
		select {
		case <-ctx.Done():
			return errutil.With(ctx.Err())
		case <-c:
		}
	}
}

// Good: a non-blocking select still establishes done in its Done arm.
func goodCtxErrSelectDefault(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errutil.With(ctx.Err())
	default:
		return nil
	}
}

// Good: a single-case select compiles to a plain channel receive.
func goodCtxErrSingleCaseSelect(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errutil.With(ctx.Err())
	}
}

// Good: a context held in a struct field — each access is a distinct SSA value,
// matched structurally.
type ctxHolder struct{ ctx context.Context }

func goodCtxErrFieldDone(h ctxHolder) error {
	<-h.ctx.Done()
	return errutil.With(h.ctx.Err())
}

func goodCtxErrPtrFieldDone(h *ctxHolder) error {
	<-h.ctx.Done()
	return errutil.With(h.ctx.Err())
}

// Bad: different fields of the same struct are different contexts.
type twoCtxHolder struct{ a, b context.Context }

func badCtxErrDifferentField(h twoCtxHolder) error {
	<-h.a.Done()
	return errutil.With(h.b.Err()) // want `do not directly wrap`
}

// Good: ctx.Err() checked non-nil then wrapped via a second call. Err is
// monotone ("successive calls to Err return the same error"), so the second
// call returns the same non-nil error.
func goodCtxErrDoubleCall(ctx context.Context) error {
	if ctx.Err() != nil {
		return errutil.With(ctx.Err())
	}
	return nil
}

// Good: the check through a local variable still vouches for a fresh call.
func goodCtxErrCheckedVar(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return errutil.With(ctx.Err())
	}
	return nil
}

// Good: an errors.Is guard on one call vouches for the next.
func goodCtxErrErrorsIs(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return errutil.With(ctx.Err())
	}
	return nil
}

// Good: monotonicity composes with field-stored contexts.
func goodCtxErrFieldDoubleCall(h ctxHolder) error {
	if h.ctx.Err() != nil {
		return errutil.With(h.ctx.Err())
	}
	return nil
}

// Bad: the == nil branch does not vouch — the context may become done between
// the check and the wrapped call, but at check time it was nil.
func badCtxErrCheckedNilBranch(ctx context.Context) error {
	if ctx.Err() == nil {
		return errutil.With(ctx.Err()) // want `do not directly wrap`
	}
	return nil
}

// Bad: a check on a different context does not vouch.
func badCtxErrDoubleCallDifferentCtx(ctx1, ctx2 context.Context) error {
	if ctx1.Err() != nil {
		return errutil.With(ctx2.Err()) // want `do not directly wrap`
	}
	return nil
}

// Good: a directive on a later line of a multiline wrap call suppresses (the
// diagnostic position is the call's opening paren, but the directive may sit
// anywhere within the call's span).
func goodDirectiveMultilineArgLine() error {
	return errutil.With(
		returnsErr()) //errutil:unchecked
}

// A directive on the line below a single-line wrap call does not suppress; the
// span only extends downward for calls that actually span multiple lines.
func badDirectiveBelow() error {
	return errutil.With(returnsErr()) // want `do not directly wrap`
	//errutil:unchecked // want `unused errutil:unchecked directive`
}

func exits() { os.Exit(1) }

// Good (by construction): code after a call to a never-returning function is
// pruned as unreachable by the SSA builder (x/tools ≥ v0.45 interprocedural
// no-return analysis), so the wrap can never execute and is not flagged.
func goodWrapAfterNoReturn() {
	exits()
	_ = errutil.With(returnsErr())
}

// In a nested multiline wrap only the inner call is flagged (the outer wraps a
// non-nil Wrap result), and a directive on the inner call's line suppresses it.
func goodDirectiveNestedMultiline() error {
	return errutil.With(
		errutil.Wrap(returnsErr()), //errutil:unchecked
	)
}

// Limitation: field-based context matching assumes the field is not reassigned
// between the Done receive and the wrap, so this reassignment goes undetected
// (contexts stored in fields are overwhelmingly write-once).
func goodCtxFieldReassignLimitation(ctx1, ctx2 context.Context) error {
	var h ctxHolder
	h.ctx = ctx1
	<-h.ctx.Done()
	h.ctx = ctx2
	return errutil.With(h.ctx.Err())
}

// isFooErr returns true only for non-nil errors: the nil case returns false
// before any inspection, so its true branch works as a nil check.
func isFooErr(err error) bool {
	if err == nil {
		return false
	}
	return err.Error() == "foo"
}

// Good: a predicate helper's true branch establishes non-nil.
func goodPredicateHelper() error {
	err := returnsErr()
	if isFooErr(err) {
		return errutil.With(err)
	}
	return nil
}

// Good: the predicate proves through && short-circuit conditions too.
func goodPredicateHelperAnd(other bool) error {
	err := returnsErr()
	if other && isFooErr(err) {
		return errutil.With(err)
	}
	return nil
}

// truthy can return true for a nil error, so it proves nothing.
func truthy(error) bool {
	return true
}

// Bad: a predicate without a nil guard does not establish non-nil.
func badPredicateNoGuard() error {
	err := returnsErr()
	if truthy(err) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// mergedPredicate returns a value merged from guarded and unguarded paths (an
// SSA phi); the value-level proof accepts it: the unguarded incoming is
// constant false, and the guarded incoming arrives from the err != nil branch.
func mergedPredicate(err error, other bool) bool {
	return err != nil && other
}

// Good: the merged-value predicate establishes non-nil.
func goodMergedPredicate() error {
	err := returnsErr()
	if mergedPredicate(err, true) {
		return errutil.With(err)
	}
	return nil
}

// reversedMergedPredicate puts the nil check in the merged value itself rather
// than the guarding branch.
func reversedMergedPredicate(err error, other bool) bool {
	return other && err != nil
}

// Good: the reversed merged-value predicate establishes non-nil too.
func goodReversedMergedPredicate() error {
	err := returnsErr()
	if reversedMergedPredicate(err, true) {
		return errutil.With(err)
	}
	return nil
}

// isNamedErr mirrors the comma-ok-and-condition helper shape: true requires
// ok, and ok (an errors.AsType match) implies a non-nil err — no explicit nil
// guard needed.
func isNamedErr(err error, name string) bool {
	cErr, ok := errors.AsType[*customErr](err)
	return ok && cErr.Error() == name
}

// Good: the comma-ok-and-condition predicate establishes non-nil.
func goodCommaOkPredicate() error {
	err := returnsErr()
	if isNamedErr(err, "x") {
		return errutil.With(err)
	}
	return nil
}

// orPredicate can return true for a nil error (when other is true), so it
// proves nothing.
func orPredicate(err error, other bool) bool {
	return err != nil || other
}

// Bad: the || merge does not establish non-nil.
func badOrPredicate() error {
	err := returnsErr()
	if orPredicate(err, true) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// negatedPredicate proves through negation of a recognized condition.
func negatedPredicate(err error) bool {
	return !(err == nil)
}

// Good: the negated predicate establishes non-nil.
func goodNegatedPredicate() error {
	err := returnsErr()
	if negatedPredicate(err) {
		return errutil.With(err)
	}
	return nil
}

// isSentinelErr forwards to errors.Is with a never-nil package sentinel: the
// target is provably non-nil, so the predicate is recognized without an
// explicit nil guard.
func isSentinelErr(err error) bool {
	return errors.Is(err, sentinel)
}

// Good: the sentinel-target predicate establishes non-nil.
func goodSentinelTargetPredicate() error {
	err := returnsErr()
	if isSentinelErr(err) {
		return errutil.With(err)
	}
	return nil
}

// isSentinelEqual compares against the proven sentinel: equality with a
// provably non-nil value needs no branch-position trust.
func isSentinelEqual(err error) bool {
	return err == sentinel
}

// Good: equality against a proven sentinel establishes non-nil.
func goodSentinelEqualPredicate() error {
	err := returnsErr()
	if isSentinelEqual(err) {
		return errutil.With(err)
	}
	return nil
}

// isForwardedTarget forwards its own parameter as the errors.Is target;
// isForwardedTarget(nil, nil) is true, so it proves nothing.
func isForwardedTarget(err, target error) bool {
	return errors.Is(err, target)
}

// Bad: the forwarded-target predicate is not recognized.
func badForwardedTargetPredicate() error {
	err := returnsErr()
	if isForwardedTarget(err, nil) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// errMutable is assigned nil outside initialization, so it is not a sentinel.
var errMutable = errors.New("mutable")

func resetMutable() {
	errMutable = nil
}

func isMutableErr(err error) bool {
	return errors.Is(err, errMutable)
}

// Bad: a target var with a nil store somewhere is not a sentinel.
func badMutableTargetPredicate() error {
	err := returnsErr()
	resetMutable()
	if isMutableErr(err) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// errEscapes has its address passed away, so its stores cannot be tracked.
var errEscapes = errors.New("escapes")

func escapeVar(p *error) {
	_ = p
}

func init() {
	escapeVar(&errEscapes)
}

func isEscapedErr(err error) bool {
	return errors.Is(err, errEscapes)
}

// Bad: an address-escaping target var is not a sentinel.
func badEscapedTargetPredicate() error {
	err := returnsErr()
	if isEscapedErr(err) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// sentinelEqualPredicate returns equality against a possibly-nil parameter.
// Sentinel equality is only trusted in branch position, never as a returned
// value — errors.Is's own body ends in this shape and must not get a fact.
func sentinelEqualPredicate(err, target error) bool {
	return err == target
}

// Bad: returned sentinel equality proves nothing (true for nil == nil).
func badSentinelEqualPredicate() error {
	err := returnsErr()
	if sentinelEqualPredicate(err, nil) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// loopPredicate accumulates its result through a loop phi cycle, proved
// inductively over iterations: res starts false and can only become true via
// err != nil, with the res-was-already-true edge covered by the induction
// hypothesis.
func loopPredicate(err error, n int) bool {
	res := false
	for i := 0; i < n; i++ {
		res = res || err != nil
	}
	return res
}

// Good: the loop-accumulator predicate establishes non-nil.
func goodLoopPredicate() error {
	err := returnsErr()
	if loopPredicate(err, 3) {
		return errutil.With(err)
	}
	return nil
}

// loopUnrelated can become true from a value unrelated to err, so the
// induction has no sound base case.
func loopUnrelated(err error, other bool, n int) bool {
	res := false
	for i := 0; i < n; i++ {
		res = res || other
	}
	_ = err
	return res
}

// Bad: a loop accumulating an unrelated condition proves nothing.
func badLoopUnrelatedPredicate() error {
	err := returnsErr()
	if loopUnrelated(err, true, 3) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// loopFlip negates its accumulator each iteration, so the induction
// hypothesis (stated for true) cannot discharge the flipped edge.
func loopFlip(err error, n int) bool {
	res := err != nil
	for i := 0; i < n; i++ {
		res = !res
	}
	return res
}

// Bad: a flipping accumulator can be true on iterations where err is nil.
func badLoopFlipPredicate() error {
	err := returnsErr()
	if loopFlip(err, 3) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// fooChecker.Is shows predicate methods work; the receiver shifts the error to
// ssa argument 1. Its exported name also gets a fact (Exported is name-based).
type fooChecker struct{}

func (fooChecker) Is(err error) bool { // want Is:`nonNilWhenTrue\[1\]`
	if err == nil {
		return false
	}
	return true
}

// Good: predicate methods establish non-nil like predicate functions.
func goodPredicateMethod() error {
	err := returnsErr()
	var f fooChecker
	if f.Is(err) {
		return errutil.With(err)
	}
	return nil
}

// Bad: the predicate guards a different error than the wrapped one.
func badPredicateDifferentErr() error {
	err1 := returnsErr()
	err2 := returnsErr()
	if isFooErr(err1) {
		return errutil.With(err2) // want `do not directly wrap`
	}
	return nil
}

// Good: errors.AsType's ok is the generic equivalent of errors.As — a nil
// error matches no target type.
func goodErrorsAsTypeCheck() error {
	err := returnsErr()
	if _, ok := errors.AsType[*customErr](err); ok {
		return errutil.With(err)
	}
	return nil
}

// Bad: the AsType ok guards a different error than the wrapped one.
func badErrorsAsTypeDifferentErr() error {
	err1 := returnsErr()
	err2 := returnsErr()
	if _, ok := errors.AsType[*customErr](err1); ok {
		return errutil.With(err2) // want `do not directly wrap`
	}
	return nil
}

// preservesErr returns a non-nil error whenever its argument is non-nil: the
// nil case returns nil before constructing anything, and every other path wraps
// the (now nil-checked) argument into a never-nil errutil error.
func preservesErr(e error) error {
	if e == nil {
		return nil
	}
	return errutil.With(e)
}

// Good: wrapping a nil-preserving helper's result needs no check when its
// argument is itself nil-checked.
func goodWrapPreservesErr() error {
	err := returnsErr()
	if err != nil {
		return errutil.With(preservesErr(err))
	}
	return nil
}

// Bad: the argument to the preserving helper is unchecked, so its result may be
// nil and the wrap is still flagged.
func badWrapPreservesErrUnchecked() error {
	return errutil.With(preservesErr(returnsErr())) // want `do not directly wrap`
}

// passthroughErr returns its argument unchanged, so its result is trivially
// non-nil whenever the argument is.
func passthroughErr(e error) error { return e }

// Good: a passthrough preserves nil-ness, so a nil-checked argument proves the
// wrap.
func goodWrapPassthrough() error {
	err := returnsErr()
	if err != nil {
		return errutil.With(passthroughErr(err))
	}
	return nil
}

// forwardsPreserving forwards to another local preserving helper, so the
// nil-preserving judgment carries transitively.
func forwardsPreserving(e error) error { return preservesErr(e) }

// Good: transitivity — wrapping a helper that forwards to a preserving helper.
func goodWrapForwardsPreserving() error {
	err := returnsErr()
	if err != nil {
		return errutil.With(forwardsPreserving(err))
	}
	return nil
}

var errByKey = map[string]error{}

// notPreserving can return nil for a non-nil input (a missing map key), so it
// does NOT preserve nil-ness and gets no judgment.
func notPreserving(e error) error {
	if e == nil {
		return nil
	}
	return errByKey[e.Error()]
}

// Bad: a helper that may return nil for a non-nil argument does not prove the
// wrap even when the argument is nil-checked.
func badWrapNotPreserving() error {
	err := returnsErr()
	if err != nil {
		return errutil.With(notPreserving(err)) // want `do not directly wrap`
	}
	return nil
}

// Bad: the wrap runs before the Done receive in the same block — the context
// may not be done yet when Err is evaluated.
func badCtxErrWrapBeforeDone(ctx context.Context) error {
	e := errutil.With(ctx.Err()) // want `do not directly wrap`
	<-ctx.Done()
	return e
}

// Bad: a ctx.Err() captured before the Done receive stays possibly-nil no
// matter where it is wrapped — in the same block as the receive...
func badCtxErrCapturedBeforeDone(ctx context.Context) error {
	e := ctx.Err()
	<-ctx.Done()
	return errutil.With(e) // want `do not directly wrap`
}

// ...or with the receive in a block that dominates the wrap but not the call.
func badCtxErrCapturedBeforeDoneCrossBlock(ctx context.Context, cond bool) error {
	e := ctx.Err()
	if cond {
		_ = e
	}
	<-ctx.Done()
	return errutil.With(e) // want `do not directly wrap`
}

// Good: ctx.Err() captured in the Done arm is non-nil wherever it is wrapped —
// done is established at the arm's entry, before the call.
func goodCtxErrCapturedInDoneArm(ctx context.Context, c <-chan int, cond bool) error {
	select {
	case <-ctx.Done():
		e := ctx.Err()
		if cond {
			_ = e
		}
		return errutil.With(e)
	case <-c:
		return nil
	}
}

// Good: a directive on a later line of a multiline deferred wrap suppresses
// (ssa.Defer reports the defer keyword's position, which maps to the
// statement's span).
func goodDeferMultilineDirective() {
	defer errutil.With(
		returnsErr()) //errutil:unchecked
}

// goodDirectiveInDocMiddle has its directive on a middle line of a multi-line
// doc comment; any line of the owning doc comment suppresses the function.
//errutil:unchecked
// More documentation below the directive.
func goodDirectiveInDocMiddle() error {
	return errutil.With(returnsErr())
}

// Limitation (codified): equality between two maybe-nil errors is trusted as a
// deliberate sentinel check in branch position, so the wrap is not flagged even
// though both sides may be nil (nil == nil is true).
func goodEqualMaybeNilLimitation(err2 error) error {
	err := returnsErr()
	if err == err2 {
		return errutil.With(err)
	}
	return nil
}
