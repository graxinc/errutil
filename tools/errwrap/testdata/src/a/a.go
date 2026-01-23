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
