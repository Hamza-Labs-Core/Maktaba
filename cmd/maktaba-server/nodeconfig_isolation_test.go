package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/adopt"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/ffmpeg"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/pgbundle"
)

// TestMain points MAKTABA_NODE_CONFIG at a file that never exists, so no test
// here (nor a process it spawns) reads this machine's real node config: on a
// joined machine it holds a database credential and would switch the code under
// test into node mode. A test that needs a node config sets its own (t.Setenv).
func TestMain(m *testing.M) {
	// the test binary is also the setup tests' stand-in for maktaba-api and
	// the payload's Python (setup_steps_test.go)
	if code, ok := runFakeProgram(); ok {
		os.Exit(code)
	}
	// ...and for the TV slots' streaming binary (serve_tvheld_test.go)
	if code, ok := runFakeSlot(); ok {
		os.Exit(code)
	}
	dir, err := os.MkdirTemp("", "maktaba-nodecfg-")
	if err != nil {
		panic(err)
	}
	_ = os.Setenv("MAKTABA_NODE_CONFIG", filepath.Join(dir, "node.json"))
	_ = os.Unsetenv("MAKTABA_TV_HOST_ROLE")
	// every port is free unless a test says otherwise: the result must not depend
	// on what this machine happens to run (Splunk on 8088 ...)
	portProbe = func() config.PortProbe {
		return config.PortProbe{Free: func(string) bool { return true }, Ours: func(int) bool { return false }}
	}
	// a step that starts the machine again waits for no API here
	answerFirst = 50 * time.Millisecond
	// nor on whether this machine has an ffmpeg (cmd_ffmpeg_test.go fakes its own)
	ffmpegCheck = func(_ context.Context, t ffmpeg.Tools) ffmpeg.Report {
		return ffmpeg.Report{Tools: t, Version: "6.1.2", OK: true}
	}
	// nor its scheduled tasks and processes: setup's check and finish look for an
	// install outside the app (Epic 38 story 38.13); here there is none unless a
	// test gives its own
	adoptSys = func() adopt.Sys { return &adoptFakeSys{} }
	// nor on how full this machine's disk is: a dump's room is 5% of its disk free
	// (46 GB of a 926 GB Mac's), so a restore's set aside was skipped on a fuller disk
	// and passed on a CI runner; every disk has room unless a test says otherwise
	makeRoom = func(string, pgbundle.RoomPolicy) error { return nil }
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// TestNodeConfigIsolated: the supervisor's node config is a temp path here.
func TestNodeConfigIsolated(t *testing.T) {
	if config.LocalNodeConfig().Joined() {
		t.Fatal("a test sees a joined node config: MAKTABA_NODE_CONFIG must point at a temp path")
	}
	if p := config.LocalNodeConfigPath(); !strings.HasPrefix(filepath.Clean(p), filepath.Clean(os.TempDir())) {
		t.Fatalf("node config path %q is not a temp path", p)
	}
}
