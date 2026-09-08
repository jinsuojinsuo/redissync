package redissync

import (
	"fmt"
	"runtime"
	"testing"
)

func TestGetExternalCallerFromPackageTest(t *testing.T) {
	caller, want := getExternalCallerFromTestHelper()
	if caller != want {
		t.Fatalf("caller=%q, want %q", caller, want)
	}
}

func getExternalCallerFromTestHelper() (string, string) {
	_, file, line, _ := runtime.Caller(0)
	caller := getExternalCaller()
	return caller, fmt.Sprintf("%s:%d", pathRetainRight(file, 2), line+1)
}
