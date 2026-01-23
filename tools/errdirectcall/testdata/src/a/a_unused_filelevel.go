//errdirectcall:unchecked // want `unused errdirectcall:unchecked directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelDirective(err error) error {
	return errutil.With(err)
}
