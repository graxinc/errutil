package a

import (
	"errors"
	"io"

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
	return errutil.With(returnsErr()) //errunchecked:wrap
}

// Good: directive on line above
func goodDirectiveAbove() error {
	//errunchecked:wrap
	return errutil.With(returnsErr())
}

// Good: directive on function
//errunchecked:wrap
func goodDirectiveOnFunc() error {
	return errutil.With(returnsErr())
}

// Issue 7: multi-return extraction still flagged as direct wrap without nil check
func returnsTwoValues() (int, error) { return 0, nil }

func badWrapMultiReturnExtract() error {
	_, err := returnsTwoValues()
	return errutil.With(err) // want `do not directly wrap`
}

// Issue 3: function without error return should still be checked
func badNoErrorReturn() {
	_ = errutil.With(returnsErr()) // want `do not directly wrap`
}

// Unused directive
func badUnusedDirective(err error) error {
	//errunchecked:wrap // want `unused errunchecked:wrap directive`
	return errutil.With(err)
}

// A near-miss directive name is not a real directive, so it must not suppress.
func badNearMissDirective() error {
	return errutil.With(returnsErr()) //errunchecked:wraptypo // want `do not directly wrap`
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
