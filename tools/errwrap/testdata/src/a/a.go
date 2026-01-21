package a

import (
	"errors"
	"fmt"

	"github.com/graxinc/errutil"
)

var sentinel = errors.New("sentinel")

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

// Non-error return type not checked.
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

type customErr struct{ msg string }

func (e customErr) Error() string { return e.msg }

func badCustomErr() error {
	return customErr{} // want `error should be wrapped`
}

var badFuncLit = func(err error) error {
	return err // want `error should be wrapped`
}

func badTypeAssert(err error) error {
	if e, ok := err.(*customErr); ok {
		return e // want `error should be wrapped`
	}
	return nil
}

// Each error index checked independently.
func badMultiError(a, b error) (error, error) {
	return a, errutil.With(b) // want `error should be wrapped`
}

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

func goodNamedBlankNil() (_ error) {
	return nil
}

func goodNamedBlankWrapped() (_ error) {
	return errutil.New(errutil.Tags{"k": "v"})
}

func badNamedBlankUnwrapped() (_ error) {
	return errors.New("x") // want `error should be wrapped`
}

func goodMultiNamedBlankNil() (_ int, _ error) {
	return 42, nil
}

func goodMultiNamedBlankWrapped() (_ int, _ error) {
	return 42, errutil.New(errutil.Tags{"k": "v"})
}

func badMultiNamedBlankUnwrapped() (_ int, _ error) {
	return 42, errors.New("x") // want `error should be wrapped`
}

// All phi edges wrapped.
func goodPhiAllWrapped(cond bool) error {
	var err error
	if cond {
		err = errutil.New(errutil.Tags{"k": "v"})
	}
	return err
}

// One phi edge unwrapped.
func badPhiMixed(cond bool) error {
	var err error
	if cond {
		err = errors.New("x")
	} else {
		err = errutil.New(errutil.Tags{"k": "v"})
	}
	return err // want `error should be wrapped`
}

// Last assignment is wrapped.
func goodReassignToWrapped(err error) error {
	err = errutil.With(err)
	return err
}

// Last assignment is unwrapped.
func badReassignToUnwrapped() error {
	err := errutil.New(errutil.Tags{"k": "v"})
	err = errors.New("x")
	return err // want `error should be wrapped`
}

func badFreeVar() func() error {
	err := errors.New("x")
	return func() error {
		return err // want `error should be wrapped`
	}
}

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

func goodMultiReturn() (*int, error) {
	x := 42
	return &x, nil
}

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

type errReturner struct{}

func (e errReturner) getErr() error { return nil }

func badDirectWrapCall(f func() error) error {
	return errutil.With(f()) // want `do not directly wrap`
}

func badDirectWrapMethod() error {
	r := errReturner{}
	return errutil.With(r.getErr()) // want `do not directly wrap`
}

func badDirectWrapInlineFunc() error {
	return errutil.Wrap(func() error { // want `do not directly wrap`
		return nil
	}())
}

func goodNilCheckNeq(f func() error) error {
	if err := f(); err != nil {
		return errutil.With(err)
	}
	return nil
}

func goodNilCheckNeqReversed(f func() error) error {
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

// Nested if after nil check - wrap is in a grandchild block of the nil check.
func goodNilCheckNested(f func() error, cond bool) error {
	if err := f(); err != nil {
		if cond {
			return nil
		}
		return errutil.With(err)
	}
	return nil
}

func goodPhiCycle(cond func() bool) error {
	var err error
	for cond() {
		err = err // SSA: phi [nil, phi] - self-referential
	}
	return err
}
