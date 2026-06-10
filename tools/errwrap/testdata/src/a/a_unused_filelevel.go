//errutil:unwrapped // want `unused errutil:unwrapped directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelIgnore(err error) error {
	return errutil.With(err)
}
