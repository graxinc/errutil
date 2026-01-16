package a

import (
	"errors"
	"fmt"

	"github.com/graxinc/errutil"
)

var errSentinel = errors.New("sentinel")

// Good: nil return.
func returnNil() error {
	return nil
}

// Good: errutil.With.
func returnWith(err error) error {
	return errutil.With(err)
}

// Good: errutil.Wrap.
func returnWrap(err error) error {
	return errutil.Wrap(err)
}

// Good: errutil.Witht.
func returnWitht(err error) error {
	return errutil.Witht(err, errutil.Tags{"key": "value"})
}

// Good: errutil.Wrapt.
func returnWrapt(err error) error {
	return errutil.Wrapt(err, errutil.Tags{"key": "value"})
}

// Good: errutil.New.
func returnNew() error {
	return errutil.New(errutil.Tags{"msg": "error"})
}

// Bad: returning error variable.
func returnUnwrapped(err error) error {
	return err // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: returning sentinel.
func returnSentinel() error {
	return errSentinel // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: errors.New without wrap.
func returnErrorsNew() error {
	return errors.New("bad") // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: multiple returns with unwrapped error.
func returnMultiple(err error) (int, error) {
	return 0, err // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Good: multiple returns with wrapped error.
func returnMultipleWrapped(err error) (int, error) {
	return 0, errutil.With(err)
}

// Bad: function literal with unwrapped return.
var badFuncLit = func(err error) error {
	return err // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Good: function literal with wrapped return.
var goodFuncLit = func(err error) error {
	return errutil.With(err)
}

// Good: wrapped sentinel with With.
func returnWrappedSentinelWith() error {
	return errutil.With(errSentinel)
}

// Good: wrapped sentinel with Wrap.
func returnWrappedSentinelWrap() error {
	return errutil.Wrap(errSentinel)
}

// Bad: multiple error returns, first unwrapped.
func returnTwoErrorsFirstBad(err1, err2 error) (error, error) {
	return err1, errutil.With(err2) // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: multiple error returns, second unwrapped.
func returnTwoErrorsSecondBad(err1, err2 error) (error, error) {
	return errutil.With(err1), err2 // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: multiple error returns, both unwrapped.
func returnTwoErrorsBothBad(err1, err2 error) (error, error) {
	return err1, err2 // want "error should be wrapped with errutil.With or errutil.Wrap" "error should be wrapped with errutil.With or errutil.Wrap"
}

// Good: multiple error returns, both wrapped.
func returnTwoErrorsBothGood(err1, err2 error) (error, error) {
	return errutil.With(err1), errutil.Wrap(err2)
}

// Good: nolint directive on same line suppresses error.
func returnWithNolintSameLine(err error) error {
	return err //nolint:errwrap
}

// Good: nolint directive on line above suppresses error.
func returnWithNolintLineAbove(err error) error {
	//nolint:errwrap
	return err
}

// Good: nolint directive above function suppresses all errors in function.
//
//nolint:errwrap
func returnWithNolintFunc(err error) error {
	return err
}

// Good: non-error return not checked.
func returnString() string {
	return "not an error"
}

// Bad: fmt.Errorf without wrap.
func returnFmtErrorf() error {
	return fmt.Errorf("formatted: %w", errors.New("inner")) // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Bad: method returning unwrapped error.
type handler struct{}

func (h *handler) handleError(err error) error {
	return err // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Good: method returning wrapped error.
func (h *handler) handleErrorWrapped(err error) error {
	return errutil.With(err)
}

// Bad: struct implementing error returned without wrap.
type customError struct{ msg string }

func (e customError) Error() string { return e.msg }

func returnCustomError() error {
	return customError{msg: "test"} // want "error should be wrapped with errutil.With or errutil.Wrap"
}

// Good: struct implementing error returned with wrap.
func returnCustomErrorWrapped() error {
	return errutil.Wrap(customError{msg: "test"})
}

// Bad: type assertion result returned unwrapped.
func returnTypeAssertion(err error) error {
	if e, ok := err.(*customError); ok {
		return e // want "error should be wrapped with errutil.With or errutil.Wrap"
	}
	return nil
}

// Good: type assertion result returned wrapped.
func returnTypeAssertionWrapped(err error) error {
	if e, ok := err.(*customError); ok {
		return errutil.Wrap(e)
	}
	return nil
}

// Bad: bare return with named error return.
func namedReturnBare() (err error) {
	err = errors.New("test")
	return // want "bare return with named error return; error assigned at line 179 should be wrapped"
}

// Good: bare return with no unwrapped assignment.
func namedReturnBareNoAssignment() (err error) {
	return
}

// Good: bare return with wrapped assignment.
func namedReturnBareWrapped() (err error) {
	err = errutil.With(errors.New("test"))
	return
}

// Bad: multiple bare returns in same function.
func namedReturnBareMultiple() (err error) {
	if true {
		err = errors.New("first")
		return // want "bare return with named error return; error assigned at line 197 should be wrapped"
	}
	err = errors.New("second")
	return // want "bare return with named error return; error assigned at line 197 should be wrapped" "bare return with named error return; error assigned at line 200 should be wrapped"
}

// Bad: bare return with multiple named error returns.
func namedReturnBareMultipleErrors() (err1, err2 error) {
	err1 = errors.New("first")
	err2 = errors.New("second")
	return // want "bare return with named error return; error assigned at line 206 should be wrapped" "bare return with named error return; error assigned at line 207 should be wrapped"
}

// Bad: conditional assignment - both branches could reach the bare return.
func namedReturnBareConditional(cond bool) (err error) {
	err = errors.New("first")
	if cond {
		err = errors.New("second")
	}
	return // want "bare return with named error return; error assigned at line 213 should be wrapped" "bare return with named error return; error assigned at line 215 should be wrapped"
}

func returnAfterVar() error {
	var err error
	if true {
		err = errutil.New(errutil.Tags{"foo": "bar"})
	}
	return err // want "error should be wrapped with errutil.With or errutil.Wrap"
}
