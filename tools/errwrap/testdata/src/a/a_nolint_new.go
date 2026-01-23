//errwrap:new
package a

import (
	"errors"

	"github.com/graxinc/errutil"
)

func wrapErrorsNewInNolintFile() error {
	return errutil.With(errors.New(""))
}
