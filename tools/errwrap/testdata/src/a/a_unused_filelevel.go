//errwrap:ignore // want `unused errwrap directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelIgnore(err error) error {
	if err != nil {
		return errutil.With(err)
	}
	return nil
}
