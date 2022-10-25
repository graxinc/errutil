package pkgcalledmain

import (
	"github.com/graxinc/errutil"
)

// package is here to test a package with a dir of main

func Func() error {
	err := errutil.New(errutil.Tags{"k1": "v1"})
	return errutil.With(err)
}
