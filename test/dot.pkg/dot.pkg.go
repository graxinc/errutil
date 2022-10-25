package dotpkg

import (
	"errors"
	"time"

	"github.com/graxinc/errutil"
)

type Struct struct{}

func (Struct) unexported() error {
	return errutil.New(errutil.Tags{"k1": "v1"})
}

func (s *Struct) Exported() error {
	return errutil.With(s.unexported())
}

func Stdlib() error {
	return errors.New("regular")
}

func StdlibWitht() error {
	err := errors.New("regular")
	return errutil.Witht(err, errutil.Tags{"k1": "v1"})
}

func AnonymousFunc() error {
	var err error
	func() {
		func() {
			err = errutil.New(errutil.Tags{"k1": true})
		}()
	}()
	return err
}

func AnonymousValue() error {
	return errutil.New(errutil.Tags{"k1": struct{ Field string }{"the field"}})
}

func PkgFuncs() error {
	t := time.Time{}.Add(time.Second + time.Nanosecond)

	err := errutil.New(errutil.Tags{"k1": "v1"})
	err = errutil.Witht(err, errutil.Tags{"k2": "v2", "t1": t, "sp": "has two spaces"})
	err = errutil.With(err)
	err = errutil.Wrapt(err, errutil.Tags{"k3": "v3"})
	return errutil.Wrap(err)
}

func Caller() errutil.Frame {
	return errutil.Caller(0)
}

func CallerDefer() (f errutil.Frame) {
	defer func() {
		f = errutil.Caller(0)
	}()
	return errutil.Frame{}
}

type BaserError struct {
	Field string
	base  error
}

func (e BaserError) Error() string {
	return e.Field
}

func (e BaserError) Base() error {
	return e.base
}

type BaserParentError struct {
	base error
}

func (e BaserParentError) Error() string {
	return ""
}

func (e BaserParentError) Base() error {
	return errors.New("should not be used")
}

func (e BaserParentError) Unwrap() error {
	return e.base
}

func Baser() error {
	err1 := errutil.New(errutil.Tags{"a": 1})
	err2 := BaserError{"the field", err1}
	err3 := errutil.Wrap(err2)
	err4 := BaserParentError{err3}
	return errutil.Wrap(err4)
}
