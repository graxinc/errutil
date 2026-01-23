package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/graxinc/errutil/tools/errdirectcall"
)

func main() {
	singlechecker.Main(errdirectcall.Analyzer())
}
