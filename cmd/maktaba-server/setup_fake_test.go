package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The setup steps run other programs: the payload's maktaba-api (hash-password,
// migrate, adduser, plex-link) and the payload's Python (venv_bootstrap, then
// the venv's `models download`). In these tests the test binary is both,
// chosen by MAKTABA_TEST_FAKE (TestMain calls runFakeProgram first). Each run
// appends what it was asked to MAKTABA_TEST_FAKE_LOG, one JSON line.

type fakeCall struct {
	Args  []string          `json:"args"`
	Stdin string            `json:"stdin,omitempty"`
	Files map[string]string `json:"files,omitempty"`
	DB    string            `json:"db,omitempty"`
}

// spawnsPrograms skips, under -short, a test that runs the stand-in programs:
// each one starts this test binary again, seconds in all under -race, past the
// unit tier's per-test cap. They run in the integration tier (without -short).
func spawnsPrograms(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("runs the stand-in programs; runs without -short")
	}
}

// runFakeProgram runs this test binary as a stand-in program when
// MAKTABA_TEST_FAKE names one; ok false otherwise.
func runFakeProgram() (code int, ok bool) {
	if os.Getenv("MAKTABA_TEST_FAKE") != "1" {
		return 0, false
	}
	// which program: the api's subcommands come first, Python's are -m runs
	kind := "api"
	if len(os.Args) > 1 && os.Args[1] == "-m" {
		kind = "python"
	}
	args := os.Args[1:]
	call := fakeCall{Args: args, DB: os.Getenv("DATABASE_URL"), Files: map[string]string{}}
	defer func() {
		if p := os.Getenv("MAKTABA_TEST_FAKE_LOG"); p != "" {
			f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
			if err == nil {
				_ = json.NewEncoder(f).Encode(call)
				_ = f.Close()
			}
		}
	}()
	fail := os.Getenv("MAKTABA_TEST_FAKE_FAIL")
	flag := func(name string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	switch {
	case len(args) > 0 && args[0] == "run-until-stopped":
		// a supervised process that runs until it is stopped (serve_appbundle_test.go)
		select {}
	case kind == "api" && len(args) > 0 && args[0] == "hash-password":
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		call.Stdin = strings.TrimRight(line, "\r\n")
		fmt.Println("$argon2id$v=19$m=65536,t=2,p=1$c2FsdHNhbHQ$" + fmt.Sprintf("%x", len(call.Stdin)))
		return 0, true
	case kind == "api" && len(args) > 0 && args[0] == "migrate":
		if fail == "migrate" {
			return 1, true
		}
		return 0, true
	case kind == "api" && len(args) > 0 && args[0] == "adduser":
		f := flag("--password-hash-file")
		b, _ := os.ReadFile(f)
		call.Files[f] = string(b)
		if fail == "adduser-exists" {
			return 3, true
		}
		return 0, true
	case kind == "api" && len(args) > 0 && args[0] == "plex-link":
		f := flag("--token-file")
		b, _ := os.ReadFile(f)
		call.Files[f] = string(b)
		fmt.Println(`{"server":"Home","url":"http://192.168.1.5:32400","servers":["Home","Cabin"]}`)
		return 0, true
	case kind == "python" && strings.Contains(strings.Join(args, " "), "maktaba_pipeline.venv_bootstrap"):
		if strings.Contains(strings.Join(args, " "), "--check") {
			if os.Getenv("MAKTABA_TEST_FAKE_VENV_READY") == "1" {
				return 0, true
			}
			return 10, true
		}
		for _, st := range []string{
			`{"event":"status","state":"building","step":"venv","progress":0.05,"message":"Creating the environment"}`,
			`{"event":"status","state":"building","step":"deps","progress":0.5,"message":"Collecting torch","done":60,"total":120}`,
		} {
			fmt.Println(st)
		}
		if fail == "venv" {
			fmt.Println(`{"event":"status","state":"failed","step":"deps","progress":0.5,"message":"pip failed","error":"ERROR: No matching distribution found for torch==9.9"}`)
			return 1, true
		}
		fmt.Println(`{"event":"status","state":"ready","step":"done","progress":1,"message":"Ready"}`)
		return 0, true
	case kind == "python" && len(args) > 3 && args[2] == "models" && args[3] == "download":
		fmt.Println(`{"event":"model","name":"whisper","model":"some/whisper","state":"downloading","bytes":100,"total_bytes":400}`)
		fmt.Println(`{"event":"model","name":"whisper","model":"some/whisper","state":"ready","source":"hf"}`)
		if fail == "gated" {
			fmt.Println(`{"event":"done","ok":false,"exit":3,"items":{"whisper":"ready","cohere":"needs_login"}}`)
			return 3, true
		}
		fmt.Println(`{"event":"done","ok":true,"exit":0,"items":{"whisper":"ready","cohere":"ready"}}`)
		return 0, true
	}
	fmt.Fprintf(os.Stderr, "fake %s: unexpected %q\n", kind, args)
	return 2, true
}

// copyTestBinary puts this test binary at dst (a stand-in program's path).
func copyTestBinary(t *testing.T, dst string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func isWindows() bool { return runtime.GOOS == "windows" }

func goosArch() (string, string) { return runtime.GOOS, runtime.GOARCH }

// fakeCalls reads what the stand-ins were asked, in order.
func fakeCalls(t *testing.T, log string) []fakeCall {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil {
		return nil
	}
	var out []fakeCall
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var c fakeCall
		if json.Unmarshal([]byte(line), &c) == nil {
			out = append(out, c)
		}
	}
	return out
}
