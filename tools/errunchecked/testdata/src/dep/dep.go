// Package dep is imported by package b to exercise cross-package facts: its
// exported functions that always return a non-nil error get a nonNilError fact,
// which b relies on (b cannot see these bodies).
package dep

import "github.com/graxinc/errutil"

// AlwaysErr always returns a non-nil error (a constructor result).
func AlwaysErr() error { // want AlwaysErr:`nonNilError\[0\]`
	return errutil.New(nil)
}

// BoxErr returns a boxed concrete error (interface values are never nil).
func BoxErr() error { // want BoxErr:`nonNilError\[0\]`
	return &boxErr{}
}

// Transitive is non-nil because every return delegates to a non-nil function.
func Transitive() error { // want Transitive:`nonNilError\[0\]`
	return AlwaysErr()
}

// WithCode always returns a non-nil error in its second result.
func WithCode() (int, error) { // want WithCode:`nonNilError\[1\]`
	return 0, errutil.New(nil)
}

// MaybeErr can return nil, so no fact is exported for it.
func MaybeErr(b bool) error {
	if b {
		return errutil.New(nil)
	}
	return nil
}

// NilGuarded proves non-nil via a dominating nil check rather than by
// construction — the boundary-helper shape for unprovable callees.
func NilGuarded(b bool) error { // want NilGuarded:`nonNilError\[0\]`
	if err := MaybeErr(b); err != nil {
		return err
	}
	return errutil.New(nil)
}

type boxErr struct{}

func (boxErr) Error() string { return "" }

// Svc.AlwaysErr shows methods get facts like functions do.
type Svc struct{}

func (Svc) AlwaysErr() error { // want AlwaysErr:`nonNilError\[0\]`
	return errutil.New(nil)
}

// hidden is unexported, but its exported method is callable cross-package on a
// value obtained from Hidden, so it gets a fact too (Exported is name-based).
type hidden struct{}

func (hidden) AlwaysErr() error { // want AlwaysErr:`nonNilError\[0\]`
	return errutil.New(nil)
}

func Hidden() hidden { return hidden{} }

// GenericAlways shows generic functions get facts like ordinary ones.
func GenericAlways[T any]() error { // want GenericAlways:`nonNilError\[0\]`
	return errutil.New(nil)
}
