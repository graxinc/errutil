//errwrap:unwrapped // want `unused errwrap:unwrapped directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelIgnore(err error) error {
	return errutil.With(err)
}
