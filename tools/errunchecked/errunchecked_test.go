package errunchecked_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/graxinc/errutil/tools/errunchecked"
)

func TestAnalyzer(t *testing.T) {
	t.Parallel()
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, errunchecked.Analyzer(), "a", "dep", "b")
}
