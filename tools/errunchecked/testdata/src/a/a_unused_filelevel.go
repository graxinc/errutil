//errutil:unchecked // want `unused errutil:unchecked directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelDirective(err error) error {
	return errutil.With(err)
}
