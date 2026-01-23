//errunchecked:wrap
package a

import "github.com/graxinc/errutil"

func uncheckedWrapInNolintFile() error {
	return errutil.With(returnsErr())
}
