# errutil

[![Go Reference](https://pkg.go.dev/badge/github.com/graxinc/errutil.svg)](https://pkg.go.dev/github.com/graxinc/errutil)

## Why?

While numerous error packages provide rich functionality, `errutil` is the minimal (opinionated) functionality GRAX needs for error traces.
Minimal functionality leads to:
* Consistent use through a codebase.
* A faster implementation.
* No assumptions to break. For example, when errors with meaning (UserNotFound) are offered.

## Future improvements

* Wrapping only specific errors. Currently `With` masks underlying wraps and `Wrap` lets them all through.
* Garbage reduction. Currently we maintain pointer equality in the same vein as `errors.New` as developers likely expect, however it requires heap allocation. This only shows up however in very fast loops.

## Usage

`With` and friends produce an error containing location information. The locations in a chain of errors will surface in the `Stack` produced by `BuildStack`. The stack can then be logged, displayed, sent to a service etc.

Function calls should look similar to:
```
if err := aFunction(); err != nil {
    return errutil.With(err)
}
```

Custom errors should implement Baser or errors.Unwrap to maintain traces, similar to:
```
type CustomError struct {
    Err error
    Field string
}

func (e CustomError) Error() string {
    return "field: " + e.Field
}

func (e CustomError) Base() error {
    return e.Err
}
```

Simple logging could be done with:
```
if err := topOfCalls(); err != nil {
    log.Println(errutil.BuildStack(err))
}
```
