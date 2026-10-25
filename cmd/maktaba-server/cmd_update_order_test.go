package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// `maktaba-server update` passes an older build of the installed release's line by (up to
// date), and refuses anything else as before.
func TestUpdateCommandPassesAnOlderBuild(t *testing.T) {
	var out bytes.Buffer
	if !olderBuild(&out, "v0.3.0", "0.3.0-rc.7", &releaseverify.Error{Kind: releaseverify.ErrOlderBuild}) ||
		!strings.Contains(out.String(), "You are up to date") {
		t.Fatalf("an older build: %q", out.String())
	}
	if olderBuild(&out, "v0.3.0", "0.3.0-rc.7", &releaseverify.Error{Kind: releaseverify.ErrReplay}) ||
		olderBuild(&out, "v0.3.0", "", errors.New("download failed")) || olderBuild(&out, "v0.3.0", "", nil) {
		t.Fatal("another error passed by")
	}
}
