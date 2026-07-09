# errutil

[![Go Reference](https://pkg.go.dev/badge/github.com/graxinc/errutil.svg)](https://pkg.go.dev/github.com/graxinc/errutil)

## Why?

While numerous error packages provide rich functionality, `errutil` is the minimal (opinionated) functionality GRAX needs for error traces.
Minimal functionality leads to:

* Consistent use through a codebase.
* A faster implementation.
* No assumptions to break. For example, when errors with meaning (UserNotFound) are offered.

## Usage

`With` and friends produce an error containing location information. The locations in a chain of errors will surface in the `Stack` produced by `BuildStack`. The stack can then be logged, displayed, sent to a service etc.

The `Wrap` methods additionally wrap passed errors so `errors.Is` matches the original error. To understand when `Wrap` should be used instead of `With`, read the [Whether to Wrap](https://go.dev/blog/go1.13-errors#whether-to-wrap) section of the Go 1.13 errors blog post.

Functions that do not expose Is/As errors as part of their contract, should look similar to:

```go
func aFunc() error {
    ...
    return errutil.With(err)
}
```

Wrapping sentinel errors, should look similar to:

```go
var ErrNotFound = errors.New("not found")

...
func aFunc() error {
    if err := bFunc(); err != nil {
        return errutil.Wrap(err, ErrNotFound)
    }
    ...
}

if err := aFunc(); err != nil {
    if errors.Is(err, ErrNotFound) {
        // handle not found case
    }
    ...
}
```

Wrapping unknown errors (discouraged), should look similar to:

```go
if err := aFunc(); err != nil {
    return errutil.Wrap(err)
}
```

Wrapping custom errors that are not sentinels, should look similar to:

```go
type CustomError struct {
    Text string
}

func (e CustomError) Error() string {
    return "text: " + e.Text
}

// Must use a pointer to avoid accidental matches. Does not need to be the same type as CustomError.
var ErrCustom = errors.New("custom error")

func (CustomError) Is(target error) bool {
    return target == ErrCustom
}

func aFunc() error {
    if err := bFunc(); err != nil {
        return errutil.Wrap(err, ErrCustom)
    }
    ... 
}

if err := aFunc(); err != nil {
    var cErr CustomError
    if errors.As(err, &cErr) {
        // use cErr.Text
    }
    ...
}
```

Custom errors should implement Baser or errors.Unwrap to maintain traces, similar to:

```go
type CustomError struct {
    Err error
    Text string
}

func (e CustomError) Error() string {
    return "text: " + e.Text
}

func (e CustomError) Base() error {
    return e.Err
}
```

Simple logging could be done with:

```go
if err := topOfCalls(); err != nil {
    log.Println(errutil.BuildStack(err))
}
```

## Tools

The `tools/` directory is a separate Go module of static analyzers that enforce the usage patterns above.

> **Disclaimer:** these analyzers were written largely with the assistance of LLM tooling. Their SSA-based dataflow reasoning is subtle, so review and test changes with care.

**State:** two analyzers (`errwrap`, `errunchecked`), SSA-based, distributed as standalone binaries under `cmd/`. Both report unused suppression directives as failures.

The rule summaries below are intentionally brief. The `testdata/src/a/a.go` file for each analyzer is the authoritative specification; every accepted and flagged case is a labeled example, and reviewing it directly is the most complete demonstration of the rules.

### errwrap — [testdata](tools/errwrap/testdata/src/a/a.go)

Every returned error must carry errutil location info.

* **unwrapped** — a returned error that isn't wrapped (`return err`, `return errors.New(...)`, a bare field/map/index/call result).
* **new** — wrapping `errors.New`/`fmt.Errorf`; use `errutil.New` instead.

Accepted (not flagged): `nil`, any value traced back to an errutil wrap, `Base() error` accessors, and struct fields whose every assignment is wrapped.

### errunchecked — [testdata](tools/errunchecked/testdata/src/a/a.go)

* **unchecked** — `errutil.With`/`Wrap` applied to a call result with no preceding nil check (e.g. `return errutil.With(f())`), which would wrap a possibly-nil error.

Accepted: the wrap sits behind a recognized nil check — `err != nil`, sentinel equality, `errors.Is`/`As`, comma-ok assertions, `ctx.Err()`, provably-non-nil bool predicates, or a nil-preserving helper.

### Suppressing

Place `//errutil:unwrapped`, `//errutil:new`, or `//errutil:unchecked` on the statement (any line of a multiline call/return), the line above it, on the function, or before the `package` declaration (file-wide). Generated files (`// Code generated ... DO NOT EDIT.`) are not checked, though their functions still contribute non-nil facts to errunchecked.

### Install & run

```bash
go install github.com/graxinc/errutil/tools/errwrap/cmd/errwrap@latest
go install github.com/graxinc/errutil/tools/errunchecked/cmd/errunchecked@latest

errwrap ./...
errunchecked ./...
```

## Future improvements

* Garbage reduction. Currently we maintain pointer equality in the same vein as `errors.New` as developers likely expect, however it requires heap allocation. This only shows up however in very fast loops.
