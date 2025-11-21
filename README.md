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

## Future improvements

* Garbage reduction. Currently we maintain pointer equality in the same vein as `errors.New` as developers likely expect, however it requires heap allocation. This only shows up however in very fast loops.
