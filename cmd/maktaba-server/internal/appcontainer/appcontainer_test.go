package appcontainer

import (
	"runtime"
	"testing"
)

func TestOtherSystemsAreRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows runs the real code (plugins-sandbox)")
	}
	if _, err := Run(Profile, "x", nil, Options{}); err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
	if err := Grant(Profile, ".", false); err != ErrUnsupported {
		t.Fatalf("got %v", err)
	}
}
