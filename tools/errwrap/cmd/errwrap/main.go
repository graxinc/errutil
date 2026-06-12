package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/graxinc/errutil/tools/errwrap"
)

func main() {
	singlechecker.Main(errwrap.Analyzer())
}
