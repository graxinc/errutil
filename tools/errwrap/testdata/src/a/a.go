package a

import (
	"errors"
	"fmt"
	"iter"

	"github.com/graxinc/errutil"
)

var sentinel = errors.New("sentinel")

// Good: wrapped returns
func goodWrapped(err error) error {
	return errutil.With(err)
}

func goodNew() error {
	return errutil.New(errutil.Tags{"k": "v"})
}

func goodNil() error {
	return nil
}

func goodNonError() string {
	return "not an error"
}

// Good: directive on same line
func goodDirectiveSameLine(err error) error {
	return err //errwrap:unwrapped
}

// Good: directive on line above
func goodDirectiveAbove(err error) error {
	//errwrap:unwrapped
	return err
}

// Good: directive on function
//errwrap:unwrapped
func goodDirectiveOnFunc(err error) error {
	return err
}

// A directive on a one-line function must not leak to the adjacent function below it.
func goodDirectiveOneLiner(err error) error  { return err } //errwrap:unwrapped
func badAdjacentToDirective(err error) error { return err } // want `error should be wrapped`

// A near-miss directive name is not a real directive, so it must not suppress
// (the error still fires) and must not be reported as an unused directive.
func badNearMissDirective(err error) error {
	return err //errwrap:unwrappedtypo // want `error should be wrapped`
}

// Bad: unwrapped returns
func badParameter(err error) error {
	return err // want `error should be wrapped`
}

func badGlobal() error {
	return sentinel // want `error should be wrapped`
}

func badErrorsNew() error {
	return errors.New("") // want `error should be wrapped`
}

func badFmtErrorf() error {
	return fmt.Errorf("") // want `error should be wrapped`
}

var badFuncLit = func(err error) error {
	return err // want `error should be wrapped`
}

type customErr struct{}

func (customErr) Error() string { return "" }

func badCustomErr() error {
	return customErr{} // want `error should be wrapped`
}

func badTypeAssert(err error) error {
	if e, ok := err.(*customErr); ok {
		return e // want `error should be wrapped`
	}
	return nil
}

func badMultiError(a, b error) (error, error) {
	return a, errutil.With(b) // want `error should be wrapped`
}

// Named returns
func goodNamedNil() (err error) {
	return
}

func goodNamedWrapped() (err error) {
	err = errutil.New(errutil.Tags{})
	return
}

func badNamedBare() (err error) {
	err = errors.New("")
	return // want `error should be wrapped`
}

// Phi nodes
func badPhiMixed(cond bool) error {
	var err error
	if cond {
		err = errors.New("")
	} else {
		err = errutil.New(errutil.Tags{})
	}
	return err // want `error should be wrapped`
}

// Reassignment
func goodReassignToWrapped(err error) error {
	err = errutil.With(err)
	return err
}

func badReassignToUnwrapped() error {
	err := errutil.New(errutil.Tags{})
	err = errors.New("")
	return err // want `error should be wrapped`
}

func sinkErr(*error) {}

// Addr-taken named return reassigned to a wrapped value: the final (dominating)
// store is wrapped, so this must NOT be flagged even though an earlier store was unwrapped.
func goodReassignAddrTaken() (err error) {
	err = errors.New("")
	err = errutil.With(err)
	sinkErr(&err)
	return
}

// Addr-taken named return whose final store is unwrapped: must be flagged.
func badReassignAddrTaken() (err error) {
	err = errutil.New(errutil.Tags{})
	err = sentinel
	sinkErr(&err)
	return // want `error should be wrapped`
}

// Free variables (closure)
func badFreeVar() func() error {
	err := errors.New("")
	return func() error {
		return err // want `error should be wrapped`
	}
}

// Field/index/map access
type errHolder struct{ err error }

func badFieldAccess() error {
	return errHolder{}.err // want `error should be wrapped`
}

func badIndexAccess(errs []error) error {
	return errs[0] // want `error should be wrapped`
}

func badMapLookup(m map[string]error) error {
	return m["k"] // want `error should be wrapped`
}

// Range-over-func (iter.Seq)
func goodIterSeq(rows iter.Seq[error]) error {
	for err := range rows {
		if err != nil {
			return errutil.With(err)
		}
	}
	return nil
}

// Unused directive
func badUnusedDirective(err error) error {
	//errwrap:unwrapped // want `unused errwrap:unwrapped directive`
	return errutil.With(err)
}

// Wrapping error constructors (errwrap:new rule)
func badWrapErrorsNew() error {
	return errutil.With(errors.New("")) // want `use errutil.New instead`
}

func badWrapFmtErrorf() error {
	return errutil.Wrap(fmt.Errorf("")) // want `use errutil.New instead`
}

// Good: directive suppresses errwrap:new
//errwrap:new
func goodSuppressedNew() error {
	return errutil.With(errors.New(""))
}

func badUnusedNewDirective(err error) error {
	//errwrap:new // want `unused errwrap:new directive`
	return errutil.With(err)
}

// Issue 4: second error in (error, error) return should also be checked
func badMultiErrorSecond(a, b error) (error, error) {
	return errutil.With(a), b // want `error should be wrapped`
}

// Issue 10: Witht/Wrapt wrapping constructors should trigger errwrap:new
func badWithtErrorsNew() error {
	return errutil.Witht(errors.New(""), errutil.Tags{}) // want `use errutil.New instead`
}

func badWraptFmtErrorf() error {
	return errutil.Wrapt(fmt.Errorf(""), errutil.Tags{}) // want `use errutil.New instead`
}

// Good: Witht/Wrapt count as wrapped returns like With/Wrap.
func goodWitht(err error) error {
	return errutil.Witht(err, errutil.Tags{})
}

func goodWrapt(err error) error {
	return errutil.Wrapt(err, errutil.Tags{})
}

// Bad: forwarding a non-errutil call result directly.
func helperErr() error { return nil }

func badForwardCall() error {
	return helperErr() // want `error should be wrapped`
}

// Bad: forwarding a multi-result call's error result.
func helperTwo() (int, error) { return 0, nil }

func badForwardExtract() (int, error) {
	return helperTwo() // want `error should be wrapped`
}

// Bad: errors.Join is not a wrap.
func badJoinReturn(a, b error) error {
	return errors.Join(a, b) // want `error should be wrapped`
}

// Bad: generic functions are checked like ordinary ones.
func badGenericReturn[T any]() error {
	return errors.New("") // want `error should be wrapped`
}

// Bad: methods are checked like functions.
type svc struct{}

func (svc) badMethodReturn() error {
	return errors.New("") // want `error should be wrapped`
}

// Good: the idiomatic defer-wrap of a named result. The deferred closure runs
// after every return, so the value callers observe is wrapped even though the
// store before the return is not.
func goodDeferWrapNamed() (err error) {
	defer func() { err = errutil.With(err) }()
	err = errors.New("")
	return
}

// Good: a conditional store inside the deferred closure still wraps (the only
// store to the result is wrapped; the skip path leaves it untouched).
func goodDeferWrapNamedConditional() (err error) {
	defer func() {
		if err != nil {
			err = errutil.With(err)
		}
	}()
	err = errors.New("")
	return
}

// Bad: a deferred closure overwriting the result with an unwrapped value is
// what callers observe, even though the store before the return is wrapped.
func badDeferOverwriteUnwrapped() (err error) {
	defer func() { err = sentinel }()
	err = errutil.New(errutil.Tags{})
	return // want `error should be wrapped`
}

// Bad: a conditionally registered defer cannot vouch for every path.
func badConditionalDeferWrap(cond bool) (err error) {
	if cond {
		defer func() { err = errutil.With(err) }()
	}
	err = errors.New("")
	return // want `error should be wrapped`
}

// Bad: defer and go statements wrapping a constructor trigger errwrap:new too.
func badDeferNew() {
	defer errutil.With(errors.New("")) // want `use errutil.New instead`
}

func badGoNew() {
	go errutil.With(errors.New("")) // want `use errutil.New instead`
}

// Bad: the errwrap:new rule applies in functions without error results.
func badWrapNewNoErrorReturn() {
	_ = errutil.With(errors.New("")) // want `use errutil.New instead`
}

// Good: a directive on a later line of a multiline wrap-of-constructor call
// suppresses (the diagnostic position is the call's opening paren, but the
// directive may sit anywhere within the call's span).
func goodMultilineNewDirective() error {
	return errutil.With(
		errors.New("")) //errwrap:new
}

// Good: a directive on a later line of a multiline return statement suppresses.
func goodMultilineReturnDirective(a, b error) (error, error) {
	return errutil.With(a),
		b //errwrap:unwrapped
}

// A directive on the line below a single-line return does not suppress; the
// span only extends downward for statements that actually span multiple lines.
func badDirectiveBelowReturn(err error) error {
	return err // want `error should be wrapped`
	//errwrap:unwrapped // want `unused errwrap:unwrapped directive`
}
