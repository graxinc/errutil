package errutil_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/graxinc/errutil"
	dotpkg "github.com/graxinc/errutil/test/dot.pkg"
	pkgcalledmain "github.com/graxinc/errutil/test/main"

	"github.com/google/go-cmp/cmp"
)

func init() {
	errutil.ImportPrefix = "github.com/graxinc/"
}

func TestCaller(t *testing.T) {
	// not parallel since touching ImportPrefix.
	old := errutil.ImportPrefix
	defer func() { errutil.ImportPrefix = old }()
	errutil.ImportPrefix = "gith"

	frame := dotpkg.Caller()
	pkg, fn, fileName, line := frame.Location()

	if pkg != "ub.com/graxinc/errutil/test/dot.pkg" {
		t.Fatal(pkg)
	}
	if fn != "Caller" {
		t.Fatal(fn)
	}
	if fileName != "dot.pkg.go" {
		t.Fatal(fileName)
	}
	if line != 65 {
		t.Fatal(line)
	}
}

func TestCallerDefer(t *testing.T) {
	t.Parallel()

	frame := dotpkg.CallerDefer()
	pkg, fn, fileName, line := frame.Location()

	if pkg != "errutil/test/dot.pkg" {
		t.Fatal(pkg)
	}
	if fn != "CallerDefer.func1" {
		t.Fatal(fn)
	}
	if fileName != "dot.pkg.go" {
		t.Fatal(fileName)
	}
	if line != 70 {
		t.Fatal(line)
	}
}

func TestBuildStack(t *testing.T) {
	t.Parallel()

	t1 := time.Time{}.Add(time.Second + time.Nanosecond)

	cases := map[string]struct {
		err        error
		wantStack  errutil.Stack
		wantString string // first newline removed
	}{
		"pkgcalledmain": {
			pkgcalledmain.Func(),
			errutil.Stack{
				{Pkg: "errutil/test/main", Func: "Func", File: "pkgcalledmain.go", Line: 11},
				{Pkg: "errutil/test/main", Func: "Func", File: "pkgcalledmain.go", Line: 10, Values: errutil.Tags{"k1": "v1"}},
			},
			`
errutil/test/main pkgcalledmain.go:11 Func
errutil/test/main pkgcalledmain.go:10 Func
	k1=v1`,
		},
		"Struct": {
			(&dotpkg.Struct{}).Exported(),
			errutil.Stack{
				{Pkg: "errutil/test/dot.pkg", Func: "(*Struct).Exported", File: "dot.pkg.go", Line: 17},
				{Pkg: "errutil/test/dot.pkg", Func: "Struct.unexported", File: "dot.pkg.go", Line: 13, Values: errutil.Tags{"k1": "v1"}},
			},
			`
errutil/test/dot.pkg dot.pkg.go:17 (*Struct).Exported
errutil/test/dot.pkg dot.pkg.go:13 Struct.unexported
	k1=v1`,
		},
		"Stdlib": {
			dotpkg.Stdlib(),
			errutil.Stack{
				{Values: errutil.Tags{"msg": "regular"}},
			},
			`
	msg=regular`,
		},
		"StdlibWitht": {
			dotpkg.StdlibWitht(),
			errutil.Stack{
				{Pkg: "errutil/test/dot.pkg", Func: "StdlibWitht", File: "dot.pkg.go", Line: 26, Values: errutil.Tags{"k1": "v1"}},
				{Pkg: "errutil/test/dot.pkg", Func: "StdlibWitht", File: "dot.pkg.go", Line: 26, Values: errutil.Tags{"msg": "regular"}},
			},
			`
errutil/test/dot.pkg dot.pkg.go:26 StdlibWitht
	k1=v1
errutil/test/dot.pkg dot.pkg.go:26 StdlibWitht
	msg=regular`,
		},
		"AnonymousFunc": { // ends up inlining the dot.pkg.AnonymousFunc frame.
			dotpkg.AnonymousFunc(),
			errutil.Stack{
				{Pkg: "errutil_test", Func: "TestBuildStack.AnonymousFunc.func2.1", File: "dot.pkg.go", Line: 33, Values: errutil.Tags{"k1": true}},
			},
			`
errutil_test dot.pkg.go:33 TestBuildStack.AnonymousFunc.func2.1
	k1=true`,
		},
		"AnonymousValue": {
			dotpkg.AnonymousValue(),
			errutil.Stack{
				{Pkg: "errutil/test/dot.pkg", Func: "AnonymousValue", File: "dot.pkg.go", Line: 40, Values: errutil.Tags{"k1": struct{ Field string }{Field: "the field"}}},
			},
			`
errutil/test/dot.pkg dot.pkg.go:40 AnonymousValue
	k1={Field:the field}`,
		},
		"PkgFuncs": {
			dotpkg.PkgFuncs(),
			errutil.Stack{
				{Pkg: "errutil/test/dot.pkg", Func: "PkgFuncs", File: "dot.pkg.go", Line: 50},
				{Pkg: "errutil/test/dot.pkg", Func: "PkgFuncs", File: "dot.pkg.go", Line: 49, Values: errutil.Tags{"k3": "v3"}},
				{Pkg: "errutil/test/dot.pkg", Func: "PkgFuncs", File: "dot.pkg.go", Line: 48},
				{Pkg: "errutil/test/dot.pkg", Func: "PkgFuncs", File: "dot.pkg.go", Line: 47, Values: errutil.Tags{"k2": "v2", "t1": t1, "sp": "has two spaces"}},
				{Pkg: "errutil/test/dot.pkg", Func: "PkgFuncs", File: "dot.pkg.go", Line: 46, Values: errutil.Tags{"k1": "v1"}},
			},
			`
errutil/test/dot.pkg dot.pkg.go:50 PkgFuncs
errutil/test/dot.pkg dot.pkg.go:49 PkgFuncs
	k3=v3
errutil/test/dot.pkg dot.pkg.go:48 PkgFuncs
errutil/test/dot.pkg dot.pkg.go:47 PkgFuncs
	k2=v2 sp=has two spaces t1=0001-01-01T00:00:01.000000001Z
errutil/test/dot.pkg dot.pkg.go:46 PkgFuncs
	k1=v1`,
		},
		"WithStack": {
			dotpkg.WithStack(),
			errutil.Stack{
				{Pkg: "errutil_test", Func: "TestBuildStack", File: "errutil_test.go", Line: 153},
				{Pkg: "errutil/test/dot.pkg", Func: "WithStack", File: "dot.pkg.go", Line: 61},
				{Pkg: "errutil/test/dot.pkg", Func: "WithStack", File: "dot.pkg.go", Line: 60, Values: errutil.Tags{"k2": "v2"}},
				{Pkg: "errutil/test/dot.pkg", Func: "WithStack", File: "dot.pkg.go", Line: 59, Values: errutil.Tags{"k1": "v1"}},
			},
			`
errutil_test errutil_test.go:153 TestBuildStack
errutil/test/dot.pkg dot.pkg.go:61 WithStack
errutil/test/dot.pkg dot.pkg.go:60 WithStack
	k2=v2
errutil/test/dot.pkg dot.pkg.go:59 WithStack
	k1=v1`,
		},
		"StdlibWithStack": {
			dotpkg.StdlibWithStack(),
			errutil.Stack{
				{Pkg: "errutil_test", Func: "TestBuildStack", File: "errutil_test.go", Line: 169},
				{Pkg: "errutil/test/dot.pkg", Func: "StdlibWithStack", File: "dot.pkg.go", Line: 55},
				{Pkg: "errutil/test/dot.pkg", Func: "StdlibWithStack", File: "dot.pkg.go", Line: 55, Values: errutil.Tags{"msg": "regular"}},
			},
			`
errutil_test errutil_test.go:169 TestBuildStack
errutil/test/dot.pkg dot.pkg.go:55 StdlibWithStack
errutil/test/dot.pkg dot.pkg.go:55 StdlibWithStack
	msg=regular`,
		},
	}

	for n, c := range cases {
		c := c
		t.Run(n, func(t *testing.T) {
			t.Parallel()

			gotStack := errutil.BuildStack(c.err)

			if d := cmp.Diff(c.wantStack, gotStack); d != "" {
				t.Fatal(d)
			}

			gotString := gotStack.String()
			if d := cmp.Diff(strings.TrimPrefix(c.wantString, "\n"), gotString); d != "" {
				t.Fatal(d)
			}
		})
	}
}

func TestIs_wrapped(t *testing.T) {
	t.Parallel()

	target := errutil.New(errutil.Tags{"some": "tag"})

	errs := map[string]error{
		"wrap":              errutil.Wrap(target),
		"wrap allowed":      errutil.Wrap(target, target),
		"wrap tags":         errutil.Wrapt(target, errutil.Tags{"some": "tag"}),
		"wrap tags allowed": errutil.Wrapt(target, errutil.Tags{"some": "tag"}, target),
		"double wrap":       errutil.Wrap(errutil.Wrap(target)),
	}

	for n, err := range errs {
		t.Run(n, func(t *testing.T) {
			if !errors.Is(err, target) {
				t.Fatal("expected true")
			}
		})
	}

	err := errutil.Wrap(target, errors.New("not allowed"))
	if errors.Is(err, target) {
		t.Fatal("should not be allowed")
	}
}

func TestIs_self(t *testing.T) {
	t.Parallel()

	targets := map[string]error{
		"new":       errutil.New(errutil.Tags{"some": "tag"}),
		"with":      errutil.With(errutil.New(errutil.Tags{"some": "tag"})),
		"with tags": errutil.Witht(errutil.New(errutil.Tags{"some": "tag"}), errutil.Tags{"some": "tag"}),
		"wrap":      errutil.Wrap(errutil.New(errutil.Tags{"some": "tag"})),
	}

	for n, target := range targets {
		t.Run(n, func(t *testing.T) {
			err := target // a copy

			if !errors.Is(err, target) {
				t.Fatal("expected true")
			}
		})
	}
}

func TestBaser(t *testing.T) {
	t.Parallel()

	err := dotpkg.Baser()

	var bErr dotpkg.BaserError
	if !errors.As(err, &bErr) || bErr.Field != "the field" {
		t.Fatal(err)
	}

	got := errutil.BuildStack(err)

	want := errutil.Stack{
		{Pkg: "errutil/test/dot.pkg", Func: "Baser", File: "dot.pkg.go", Line: 109},
		{Pkg: "errutil/test/dot.pkg", Func: "Baser", File: "dot.pkg.go", Line: 109, Values: errutil.Tags{"type": "dotpkg.BaserParentError"}},
		{Pkg: "errutil/test/dot.pkg", Func: "Baser", File: "dot.pkg.go", Line: 107},
		{Pkg: "errutil/test/dot.pkg", Func: "Baser", File: "dot.pkg.go", Line: 107, Values: errutil.Tags{"msg": "the field", "type": "dotpkg.BaserError"}},
		{Pkg: "errutil/test/dot.pkg", Func: "Baser", File: "dot.pkg.go", Line: 105, Values: errutil.Tags{"a": 1}},
	}
	if d := cmp.Diff(want, got); d != "" {
		t.Fatal(d)
	}
}

func BenchmarkCallerLocation(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		pkg, fn, fileName, line := errutil.Caller(0).Location()
		if pkg == "" {
			b.Fatal(pkg, fn, fileName, line)
		}
	}
}
