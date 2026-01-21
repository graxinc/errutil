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
	err = errutil.With(errors.New("x"))
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
func goodReassignToWrapped() error {
	err := errors.New("x")
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
