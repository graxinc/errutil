package errutil

import (
	"fmt"
	"net/url"
	"path"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/exp/maps"
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
}

func (e wrapError) Unwrap() error {
	return e.err
}

type frameError struct {
	f   Frame
	t   Tags
	err error
}

func NewFrameError(f Frame, t Tags, err error, wrap bool) error {
	// pointer so equality comparison will work on a copied frameError.
	e := &frameError{f, t, err}
	if wrap {
		return wrapError{e}
	}
	return e
}

func (e *frameError) Base() error {
	return e.err
}

func (e *frameError) Error() string {
	return BuildStack(e).String()
}

type Tags map[string]any

// Will implement Unwrap. Preference should be given to With.
func Wrap(err error) error {
	return NewFrameError(Caller(1), nil, err, true)
}

func Wrapt(err error, t Tags) error {
	return NewFrameError(Caller(1), t, err, true)
}

// Will not implement Unwrap.
func With(err error) error {
	return NewFrameError(Caller(1), nil, err, false)
}

func Witht(err error, t Tags) error { //nolint:misspell
	return NewFrameError(Caller(1), t, err, false)
}

func New(t Tags) error {
	return NewFrameError(Caller(1), t, nil, false)
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
	keys := maps.Keys(f.Values)
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

		switch b := err.(type) {
		case unwraper:
			err = b.Unwrap()
		case Baser:
			err = b.Base()
		default:
			err = nil
		}
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
