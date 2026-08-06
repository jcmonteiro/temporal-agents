// Package wftest holds the small helpers the workflow test suites share, in the
// same spirit as execstoretest: one home for a stand-in or a helper every package
// needs, instead of a copy per package that drifts.
//
// It is a normal (non _test) package because Go cannot import another package's
// test files.
package wftest

import (
	"reflect"
	"runtime"
	"strings"
)

// ActivityName returns the Temporal-registered activity name for an activity
// method value (e.g. a.RunDevelopAgent).
//
// Negative assertions take a method-name string — testify's AssertNotCalled passes
// for any name it does not find, so a typo would silently defeat the assertion.
// Deriving the name from the method symbol makes a typo a compile error instead.
func ActivityName(method any) string {
	full := runtime.FuncForPC(reflect.ValueOf(method).Pointer()).Name()
	full = strings.TrimSuffix(full, "-fm")
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	return full
}
