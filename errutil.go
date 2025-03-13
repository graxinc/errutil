package errutil

import (
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

// ImportPrefix strips off pkgs when formatting, to shorten.
// Only set in a pkg init().
var ImportPrefix string

// Custom error types should implement to maintain stacks.
// Only one of Baser or errors.Unwrap is needed, preferring Baser.
type Baser interface {
	Base() error
}

type unwraper interface {
	Unwrap() error
}

type wrapError struct {
	*frameError
	mode WrapMode
}

func (e wrapError) Unwrap() error {
	return e.err
}

type frameError struct {
	f   Frame
	t   Tags
	err error
}

type WrapMode byte

const (
	Basic WrapMode = 1
	Super WrapMode = 2
)

// NewFrameError enriches err with stack information from f and tags t.
// If mode is not zero, the returned error will implement Unwrap returning err.
func NewFrameError(f Frame, t Tags, err error, mode WrapMode) error {
	// pointer so equality comparison will work on a copied frameError.
	e := &frameError{f, t, err}
	if mode != 0 {
		return wrapError{e, mode}
	}
	return e
}

func (e *frameError) Unwrap() []error {
	var c any = e
	var errs []error
	for c != nil {
		if e, ok := c.(wrapError); ok {
			if e.mode == Super {
				errs = append(errs, e.err)
			}
			c = e.err
		}
		if e, ok := c.(*frameError); ok {
			c = e.err
		} else {
			c = nil
		}
	}
	return errs
}

func (e *frameError) Base() error {
	return e.err
}

func (e *frameError) Error() string {
	return BuildStack(e).String()
}

type Tags map[string]any

// Wrap enriches err with stack information from the caller of Wrap.
// The returned error will implement Unwrap (used by errors.Is/As)
// returning err if allowed is empty, or if an item in allowed matches
// err using errors.Is.
//
// Generally, Wrap should only be used when the returned error is
// expected to be used with errors.Is or similar. Non-sentinel
// errors can implement errors.Is to be used with allowed.
func Wrap(err error, allowed ...error) error {
	return wrap(err, Basic, nil, allowed...)
}

// Wrapt is like Wrap but adds tags t to the error information.
func Wrapt(err error, t Tags, allowed ...error) error {
	return wrap(err, Basic, t, allowed...)
}

func SuperWrap(err error, allowed ...error) error {
	return wrap(err, Super, nil, allowed...)
}

func SuperWrapt(err error, t Tags, allowed ...error) error {
	return wrap(err, Super, t, allowed...)
}

func wrap(err error, mode WrapMode, t Tags, allowed ...error) error {
	if len(allowed) == 0 {
		return NewFrameError(Caller(2), t, err, mode)
	}
	for _, a := range allowed {
		if errors.Is(err, a) {
			return NewFrameError(Caller(2), t, err, mode)
		}
	}
	return NewFrameError(Caller(2), t, err, 0)
}

// With enriches err with stack information from the caller of With.
// The returned error will not implement Unwrap (used by errors.Is/As).
func With(err error) error {
	return NewFrameError(Caller(1), nil, err, 0)
}

// Witht is like With but adds tags t to the error information.
func Witht(err error, t Tags) error { //nolint:misspell
	return NewFrameError(Caller(1), t, err, 0)
}

// New returns a new error with stack information from the caller of New
// and tags t.
func New(t Tags) error {
	return NewFrameError(Caller(1), t, nil, 0)
}

// WithStack returns a new error with complete stack information from
// the caller of WithStack.
//
// This is useful in cases where errors aren't or can't be created and
// passed around, such as when recovering from panics.
func WithStack(err error) error {
	frames := callers(1)
	if len(frames) <= 1 {
		return NewFrameError(frames[0], nil, err, 0)
	}
	for _, f := range frames {
		pkg, _, _, _ := f.Location()
		if pkg == "" {
			continue
		}
		err = NewFrameError(f, nil, err, 0)
	}
	return err
}

type Frame struct {
	pcs [4]uintptr
}

// Caller(0) returns the frame for the caller of Caller.
func Caller(skip int) Frame {
	var s Frame
	runtime.Callers(skip+2, s.pcs[:]) // +2 since Callers gives here passing 1, not 0.
	return s
}

// callers(0) returns the frames starting from the caller of Caller.
func callers(skip int) []Frame {
	var pcs [64]uintptr
	n := runtime.Callers(skip+2, pcs[:]) // +2 since Callers gives here passing 1, not 0.
	var frames []Frame
	for i := 0; i < n; i++ {
		frames = append(frames, Frame{pcs: [4]uintptr{pcs[i]}})
	}
	return frames
}

func (f Frame) Location() (pkg, fn, fileName string, line int) {
	frames := runtime.CallersFrames(f.pcs[:])
	for more := true; more; {
		fr, m := frames.Next()
		more = m
		if fr == (runtime.Frame{}) {
			continue
		}
		if runtimeFrameStdlib(fr) {
			continue
		}
		return runtimeFrameLocation(fr)
	}
	return "", "unknown", "", 0
}

func runtimeFrameStdlib(fr runtime.Frame) bool {
	if strings.HasPrefix(fr.Function, "main.") {
		return false
	}
	parent, _ := path.Split(fr.Function)
	return parent == ""
}

func runtimeFrameLocation(fr runtime.Frame) (pkg, fn, fileName string, line int) {
	qualifiedFn := strings.TrimPrefix(fr.Function, ImportPrefix)

	parent, child := path.Split(qualifiedFn)
	split := strings.SplitN(child, ".", 2)

	if len(split) == 1 {
		fn = split[0]
	} else {
		pkg = parent + split[0]
		if p, err := url.PathUnescape(pkg); err == nil {
			pkg = p
		}
		fn = split[1]
	}

	return pkg, fn, path.Base(fr.File), fr.Line
}

type Stack []StackFrame

func (s Stack) String() string {
	var lines []string
	for _, f := range s {
		lines = append(lines, f.String())
	}
	return strings.Join(lines, "\n")
}

type StackFrame struct {
	Pkg    string
	Func   string
	File   string
	Line   int
	Values map[string]any
}

func (f StackFrame) String() string {
	var locationParts []string
	if f.Pkg != "" {
		locationParts = append(locationParts, f.Pkg)
	}
	if f.File != "" {
		locationParts = append(locationParts, fmt.Sprintf("%v:%v", f.File, f.Line))
	}
	if f.Func != "" {
		locationParts = append(locationParts, f.Func)
	}
	locationLine := strings.Join(locationParts, " ")

	var contextPairs []string
	keys := slices.Collect(maps.Keys(f.Values))
	sort.Strings(keys)
	for _, k := range keys {
		contextPairs = append(contextPairs, k+"="+tagValue(f.Values[k]))
	}
	contextLine := strings.Join(contextPairs, " ")

	if locationLine == "" {
		return "\t" + contextLine
	}
	if contextLine == "" {
		return locationLine
	}
	return locationLine + "\n\t" + contextLine
}

// Always returns at least 1 StackFrame for a non-nil err.
// Do not call within error.Error().
func BuildStack(err error) Stack {
	var stack []StackFrame

	for err != nil {
		var sf StackFrame

		var fErr *frameError
		switch err := err.(type) {
		case *frameError:
			fErr = err
		case wrapError:
			fErr = err.frameError
		}
		if fErr != nil {
			pkg, fn, file, line := fErr.f.Location()
			sf = StackFrame{
				Pkg:    pkg,
				Func:   fn,
				File:   file,
				Line:   line,
				Values: fErr.t,
			}
		} else {
			if len(stack) > 0 {
				sf = stack[len(stack)-1]
			}

			sf.Values = Tags{}
			if msg := err.Error(); msg != "" {
				sf.Values["msg"] = msg
			}
			if t := fmt.Sprintf("%T", err); t != "*errors.errorString" {
				sf.Values["type"] = t
			}
		}

		stack = append(stack, sf)

		var candErr error
		if u, ok := err.(unwraper); ok {
			candErr = u.Unwrap()
		}
		if b, ok := err.(Baser); ok && candErr == nil {
			candErr = b.Base()
		}
		err = candErr
	}

	return stack
}

func tagValue(val any) string {
	switch v := val.(type) {
	case time.Time:
		return v.Format(time.RFC3339Nano)
	default:
		return fmt.Sprintf("%+v", val)
	}
}
