package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/graxinc/errutil/tools/errunchecked"
)

func main() {
	singlechecker.Main(errunchecked.Analyzer())
}
