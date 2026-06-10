package errwrap_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/graxinc/errutil/tools/errwrap"
)

func TestAnalyzer(t *testing.T) {
	t.Parallel()
	analysistest.Run(t, analysistest.TestData(), errwrap.Analyzer(), "a")
}
