//errdirectcall:unchecked
package a

import "github.com/graxinc/errutil"

func directCallInNolintFile() error {
	return errutil.With(returnsErr())
}
