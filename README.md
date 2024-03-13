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

Function calls should look similar to:
```
if err := aFunction(); err != nil {
    return errutil.With(err)
}

if err := bFunction(); err != nil {
    // err could be ErrUserNotFound or another sentinel error,
    // use Wrap so errors.Is(err, ErrUserNotFound) works.
    return errutil.Wrap(err)
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

## Future improvements

* Garbage reduction. Currently we maintain pointer equality in the same vein as `errors.New` as developers likely expect, however it requires heap allocation. This only shows up however in very fast loops.
