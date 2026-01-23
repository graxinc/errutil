package a

import "github.com/graxinc/errutil"

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

// Good: wrapping nil
func goodWrapNil() error {
	return errutil.With(nil)
}

// Good: directive on same line
func goodDirectiveSameLine() error {
	return errutil.With(returnsErr()) //errdirectcall:unchecked
}

// Good: directive on line above
func goodDirectiveAbove() error {
	//errdirectcall:unchecked
	return errutil.With(returnsErr())
}

// Good: directive on function
//errdirectcall:unchecked
func goodDirectiveOnFunc() error {
	return errutil.With(returnsErr())
}

// Unused directive
func badUnusedDirective(err error) error {
	//errdirectcall:unchecked // want `unused errdirectcall:unchecked directive`
	return errutil.With(err)
}
