//errutil:new // want `unused errutil:new directive`
package a

import "github.com/graxinc/errutil"

func cleanCodeInFileLevelNewDirective(err error) error {
	return errutil.With(err)
}
