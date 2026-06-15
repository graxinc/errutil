// Package b wraps results of functions defined in package dep. Because dep's
// bodies are not available here, suppression relies on the imported nonNilError
// facts; a function without a fact (dep.MaybeErr) must still be flagged.
package b

import (
	"errors"

	"dep"

	"github.com/graxinc/errutil"
)

// Good: dep.AlwaysErr has a nonNilError fact.
func goodWrapDepAlways() error {
	return errutil.With(dep.AlwaysErr())
}

// Good: dep.BoxErr (boxed concrete error) has a fact.
func goodWrapDepBox() error {
	return errutil.With(dep.BoxErr())
}

// Good: transitive non-nil is captured by the fact.
func goodWrapDepTransitive() error {
	return errutil.With(dep.Transitive())
}

// Good: the fact records the non-nil result index (1), matching the extract.
func goodWrapDepMultiReturn() error {
	_, err := dep.WithCode()
	return errutil.With(err)
}

// Bad: dep.MaybeErr has no fact, so its result may be nil.
func badWrapDepMaybe() error {
	return errutil.With(dep.MaybeErr(true)) // want `do not directly wrap`
}

// Good: method facts import like function facts.
func goodWrapDepMethod() error {
	var s dep.Svc
	return errutil.With(s.AlwaysErr())
}

// Good: an exported method on an unexported type, reached via an exported
// function, still carries its fact.
func goodWrapDepHiddenMethod() error {
	return errutil.With(dep.Hidden().AlwaysErr())
}

// Good: generic function facts import too.
func goodWrapDepGeneric() error {
	return errutil.With(dep.GenericAlways[int]())
}

// Good: the nil-guard proof carries across packages like any other fact.
func goodWrapDepNilGuarded() error {
	return errutil.With(dep.NilGuarded(true))
}

// Good: facts from generated files still vouch for handwritten callers.
func goodWrapDepGenerated() error {
	return errutil.With(dep.GeneratedAlwaysErr())
}

// Good: dep.IsDep carries a nonNilWhenTrue fact, so its true branch is a nil
// check even without the body.
func goodWrapDepPredicate() error {
	err := dep.MaybeErr(true)
	if dep.IsDep(err) {
		return errutil.With(err)
	}
	return nil
}

// Bad: dep.Truthy has no predicate fact.
func badWrapDepTruthy() error {
	err := dep.MaybeErr(true)
	if dep.Truthy(err) {
		return errutil.With(err) // want `do not directly wrap`
	}
	return nil
}

// Good: merged-condition predicate facts import like any other.
func goodWrapDepMergedPredicate() error {
	err := dep.MaybeErr(true)
	if dep.IsDepMerged(err, true) {
		return errutil.With(err)
	}
	return nil
}

// isDepTarget forwards to errors.Is with dep's exported sentinel; the
// nonNilVar fact carries the proof even though dep's body is unavailable.
func isDepTarget(err error) bool {
	return errors.Is(err, dep.ErrDep)
}

// Good: cross-package sentinel facts make the predicate provable.
func goodWrapDepSentinelPredicate() error {
	err := dep.MaybeErr(true)
	if isDepTarget(err) {
		return errutil.With(err)
	}
	return nil
}
