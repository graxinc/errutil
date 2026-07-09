// Package sentinel exercises function verdicts computed while the sentinel
// pass is mid-build: during the build every global conservatively reads as
// unproven, so any verdict computed then (here chainErr's, forced by
// errStored's initializer) is provisional and must not be memoized.
package sentinel

import "github.com/graxinc/errutil"

var errBase = errutil.New(nil)

// errStored's initializer stores chainErr's result, forcing chainErr's verdict
// to be computed during the sentinel build, where errBase is still unproven.
var errStored = chainErr()

func chainErr() error { return errBase }

// Good: chainErr always returns the non-nil sentinel errBase. The conservative
// mid-build verdict must not stick, or this wrap would be falsely flagged.
func goodWrapChain() error {
	return errutil.With(chainErr())
}
