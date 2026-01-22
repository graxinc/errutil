package a

import (
	"errors"
	"fmt"
	"iter"

	"github.com/graxinc/errutil"
)

var sentinel = errors.New("sentinel")

// Basic wrapping functions
func goodNil() error {
	return nil
}

func goodWith(err error) error {
	return errutil.With(err)
}

func goodWrap(err error) error {
	return errutil.Wrap(err)
}

func goodWitht(err error) error {
	return errutil.Witht(err, errutil.Tags{"k": "v"})
}

func goodWrapt(err error) error {
	return errutil.Wrapt(err, errutil.Tags{"k": "v"})
}

func goodNew() error {
	return errutil.New(errutil.Tags{"k": "v"})
}

func goodNonError() string {
	return "not an error"
}

func goodNolintSameLine(err error) error {
	return err //nolint:errwrap
}

func goodNolintLineAbove(err error) error {
	//nolint:errwrap
	return err
}

//nolint:errwrap
func goodNolintFunc(err error) error {
	return err
}

// Unwrapped returns
func badParameter(err error) error {
	return err // want `error should be wrapped`
}

func badGlobal() error {
	return sentinel // want `error should be wrapped`
}

func badErrorsNew() error {
	return errors.New("x") // want `error should be wrapped`
}

func badFmtErrorf() error {
	return fmt.Errorf("x") // want `error should be wrapped`
}

var badFuncLit = func(err error) error {
	return err // want `error should be wrapped`
}

type customErr struct{ msg string }

func (e customErr) Error() string { return e.msg }

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
	err = errutil.New(errutil.Tags{"k": "v"})
	return
}
func badNamedBare() (err error) {
	err = errors.New("x")
	return // want `error should be wrapped`
}

// Phi nodes
func goodPhiAllWrapped(cond bool) error {
	var err error
	if cond {
		err = errutil.New(errutil.Tags{"k": "v"})
	}
	return err
}

func badPhiMixed(cond bool) error {
	var err error
	if cond {
		err = errors.New("x")
	} else {
		err = errutil.New(errutil.Tags{"k": "v"})
	}
	return err // want `error should be wrapped`
}

func goodPhiCycle(cond func() bool) error {
	var err error
	for cond() {
		err = err
	}
	return err
}

// Reassignment
func goodReassignToWrapped(err error) error {
	err = errutil.With(err)
	return err
}

func badReassignToUnwrapped() error {
	err := errutil.New(errutil.Tags{"k": "v"})
	err = errors.New("x")
	return err // want `error should be wrapped`
}

// Free variables
func badFreeVar() func() error {
	err := errors.New("x")
	return func() error {
		return err // want `error should be wrapped`
	}
}

// Field/index/map access
type errHolder struct{ err error }

func badFieldAccess() error {
	h := errHolder{err: errors.New("x")}
	return h.err // want `error should be wrapped`
}

func badIndexAccess(errs []error) error {
	return errs[0] // want `error should be wrapped`
}

func badMapLookup(m map[string]error) error {
	return m["key"] // want `error should be wrapped`
}

// Defer with multi-return
func goodMultiReturnDefer() (*int, error) {
	x := 42
	defer func() {}()
	return &x, nil
}

func goodMultiReturnDeferMultiple(fail bool) (*int, error) {
	x := 42
	defer func() {}()
	if fail {
		return nil, errutil.New(errutil.Tags{"k": "v"})
	}
	return &x, nil
}

func badMultiReturnDeferUnwrapped(fail bool) (*int, error) {
	x := 42
	defer func() {}()
	if fail {
		return nil, errors.New("x") // want `error should be wrapped`
	}
	return &x, nil
}

// Direct wrapping of function calls (should nil check first)
type errReturner struct{}

func (e errReturner) getErr() error { return nil }

func badDirectWrapCall(f func() error) error {
	return errutil.With(f()) // want `do not directly wrap`
}

func badDirectWrapMethod() error {
	return errutil.With(errReturner{}.getErr()) // want `do not directly wrap`
}

func badDirectWrapErrorsNew() error {
	return errutil.With(errors.New("x")) // want `do not directly wrap`
}

// Nil checks before wrapping
func goodNilCheckNeq(f func() error) error {
	if err := f(); err != nil {
		return errutil.With(err)
	}
	return nil
}

func goodNilCheckReversed(f func() error) error {
	if err := f(); nil != err {
		return errutil.With(err)
	}
	return nil
}

func goodNilCheckEqElse(f func() error) error {
	if err := f(); err == nil {
		return nil
	} else {
		return errutil.With(err)
	}
}

func goodNilCheckNested(f func() error, cond bool) error {
	if err := f(); err != nil {
		if cond {
			return nil
		}
		return errutil.With(err)
	}
	return nil
}

// Range-over-func (iter.Seq) - tests SSA alloc/store handling
func goodIterSeq(rows iter.Seq[error]) error {
	for err := range rows {
		if err != nil {
			return errutil.With(err)
		}
	}
	return nil
}
