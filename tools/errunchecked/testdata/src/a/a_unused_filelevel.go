//errunchecked:wrap // want `unused errunchecked:wrap directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelDirective(err error) error {
	return errutil.With(err)
}
