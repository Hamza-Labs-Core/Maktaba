package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSteps answers steps from a table and records every call.
type fakeSteps struct {
	calls   []string
	inputs  map[string][]string
	answers map[string][]any // per step: results (or *stepFailure) in turn
}

func (f *fakeSteps) run(_ context.Context, step string, input any, progress func(stepEvent)) (json.RawMessage, error) {
	f.calls = append(f.calls, step)
	in, _ := json.Marshal(input)
	if f.inputs == nil {
		f.inputs = map[string][]string{}
	}
	f.inputs[step] = append(f.inputs[step], string(in))
	if progress != nil {
		progress(stepEvent{Type: "progress", Stage: step, Message: "working on " + step, Done: 1, Total: 2})
	}
	queue := f.answers[step]
	if len(queue) == 0 {
		return json.RawMessage(`{}`), nil
	}
	next := queue[0]
	if len(queue) > 1 {
		f.answers[step] = queue[1:]
	}
	if fail, ok := next.(*stepFailure); ok {
		return nil, fail
	}
	b, _ := json.Marshal(next)
	return b, nil
}

// script is a terminal that answers from queues and fails the test when asked
// something it has no answer for.
type script struct {
	t       *testing.T
	lines   []string
	secrets []string
	yes     []bool
}

func (s *script) line(q, def string) string {
	if len(s.lines) == 0 {
		s.t.Fatalf("unexpected question %q", q)
	}
	v := s.lines[0]
	s.lines = s.lines[1:]
	if v == "" {
		return def
	}
	return v
}

func (s *script) secret(q string) string {
	if len(s.secrets) == 0 {
		s.t.Fatalf("unexpected secret question %q", q)
	}
	v := s.secrets[0]
	s.secrets = s.secrets[1:]
	return v
}

func (s *script) yesNo(q string, def bool) bool {
	if len(s.yes) == 0 {
		s.t.Fatalf("unexpected yes/no %q", q)
	}
	v := s.yes[0]
	s.yes = s.yes[1:]
	return v
}

var checkOK = map[string]any{"ok": true, "data_dir": "/home/me/.local/share/maktaba",
	"warnings": []string{"Less than 32 GB of RAM"}, "runs": []map[string]string{{"role": "postgres", "what": "The library database"}}}

func TestAppSetupInteractive(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{
		"status":    {map[string]any{"configured": false}},
		"check":     {checkOK},
		"libraries": {&stepFailure{Code: "root_missing", Message: "/media/x does not exist", Field: "roots"}, map[string]any{}},
		"plex":      {map[string]any{"pin_id": 7, "code": "ABCD", "link_url": "https://plex.tv/link"}, map[string]any{"linked": false}, map[string]any{"linked": true}},
		"finish":    {map[string]any{"web_url": "http://127.0.0.1:8088"}},
	}}
	sc := &script{t: t,
		lines: []string{"admin", "/media/x", "", "/media/y", "/mnt/media => /srv/media"},
		// admin: a mismatch, then the same twice; models: a token
		secrets: []string{"pw-one", "pw-two", "s3cret!", "s3cret!", "hf_token"},
		yes:     []bool{true}}
	var out bytes.Buffer
	finished := false
	a := &appSetup{run: f.run, ask: sc, out: &out, pollEvery: time.Millisecond,
		afterFinish: func(context.Context) error { finished = true; return nil }}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !finished {
		t.Error("the service was not installed after finish")
	}
	want := "status check admin libraries libraries models plex plex plex venv finish"
	if got := strings.Join(f.calls, " "); got != want {
		t.Errorf("steps %q, want %q", got, want)
	}
	if got := f.inputs["admin"][0]; got != `{"password":"s3cret!","username":"admin"}` {
		t.Errorf("admin input %s", got)
	}
	if got := f.inputs["libraries"][1]; got != `{"path_map":[{"from":"/mnt/media","to":"/srv/media"}],"roots":["/media/y"]}` {
		t.Errorf("libraries input after the error %s", got)
	}
	if got := f.inputs["models"][0]; got != `{"hf_token":"hf_token","source":"huggingface"}` {
		t.Errorf("models input %s", got)
	}
	if got := f.inputs["plex"][1]; got != `{"action":"poll","pin_id":7}` {
		t.Errorf("plex poll %s", got)
	}
	for _, w := range []string{"/media/x does not exist", "Note: Less than 32 GB of RAM", "enter the code ABCD",
		"Plex is linked", "Open http://127.0.0.1:8088", "working on venv (50%)"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	if strings.Contains(out.String(), "s3cret!") || strings.Contains(out.String(), "hf_token\"") {
		t.Error("a secret was printed")
	}
}

func TestAppSetupFromAnswers(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{
		"check":  {checkOK},
		"finish": {map[string]any{"web_url": "http://127.0.0.1:8088"}},
	}}
	answers := map[string]json.RawMessage{
		"admin":     json.RawMessage(`{"username":"admin","password":"s3cret!"}`),
		"libraries": json.RawMessage(`{"roots":["/srv/media"],"path_map":[]}`),
		"models":    json.RawMessage(`{"source":"nas","path":"/srv/models"}`),
		"plex":      json.RawMessage(`{"action":"skip"}`),
	}
	a := &appSetup{run: f.run, ask: &script{t: t}, out: &bytes.Buffer{}, answers: answers}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.inputs["admin"][0]; got != `{"username":"admin","password":"s3cret!"}` {
		t.Errorf("admin input %s", got)
	}
	if got := f.inputs["plex"][0]; got != `{"action":"skip"}` {
		t.Errorf("plex %s", got)
	}

	// an answered step that fails is not retried
	f = &fakeSteps{answers: map[string][]any{"check": {checkOK},
		"admin": {&stepFailure{Code: "weak_password", Message: "too short", Field: "password"}}}}
	a = &appSetup{run: f.run, ask: &script{t: t}, out: &bytes.Buffer{}, answers: answers}
	err := a.Run(context.Background())
	var fail *stepFailure
	if !errors.As(err, &fail) || fail.Code != "weak_password" {
		t.Errorf("err = %v", err)
	}
}

func TestAppSetupStopsWhenTheComputerCannotRunIt(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{"check": {map[string]any{"ok": false, "problems": []string{"glibc 2.31, needs 2.35"}}}}}
	var out bytes.Buffer
	a := &appSetup{run: f.run, ask: &script{t: t}, out: &out}
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("setup went on")
	}
	if !strings.Contains(out.String(), "Cannot run: glibc 2.31, needs 2.35") {
		t.Errorf("output %s", out.String())
	}
	if strings.Join(f.calls, " ") != "status check" {
		t.Errorf("steps %v", f.calls)
	}
}

func TestAppSetupOnAConfiguredComputerReinstallsTheService(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{"status": {map[string]any{"configured": true, "web_url": "http://127.0.0.1:8088"}}}}
	done := false
	a := &appSetup{run: f.run, ask: &script{t: t, yes: []bool{true}}, out: &bytes.Buffer{},
		afterFinish: func(context.Context) error { done = true; return nil }}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !done || strings.Join(f.calls, " ") != "status" {
		t.Errorf("done %v, steps %v", done, f.calls)
	}
}

func TestReadStepEvents(t *testing.T) {
	var stages []string
	res, fail := readStepEvents(strings.NewReader(
		`{"type":"progress","stage":"download","message":"m","done":1,"total":2}`+"\n"+
			"not json\n"+
			`{"type":"result","data":{"ok":true}}`+"\n"+
			`{"type":"error","code":"late","message":"ignored"}`+"\n"),
		func(ev stepEvent) { stages = append(stages, ev.Stage) })
	if fail != nil || string(res) != `{"ok":true}` || strings.Join(stages, ",") != "download" {
		t.Errorf("res %s fail %v stages %v", res, fail, stages)
	}
	_, fail = readStepEvents(strings.NewReader(`{"type":"error","code":"hf_gated","message":"accept","field":"hf_token"}`+"\n"), nil)
	if fail == nil || fail.Code != "hf_gated" || fail.Field != "hf_token" {
		t.Errorf("fail %+v", fail)
	}
}

func TestReadAnswers(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.json")
	_ = os.WriteFile(good, []byte(`{"admin":{"username":"a","password":"b"},"plex":{"action":"skip"}}`), 0o600)
	m, err := readAnswers(good, 0)
	if err != nil || len(m) != 2 {
		t.Errorf("answers %v %v", m, err)
	}
	bad := filepath.Join(dir, "b.json")
	_ = os.WriteFile(bad, []byte(`{"admn":{}}`), 0o600)
	if _, err := readAnswers(bad, 0); err == nil || !strings.Contains(err.Error(), `"admn"`) {
		t.Errorf("a misspelt step: %v", err)
	}
	if m, err := readAnswers("", 0); m != nil || err != nil {
		t.Errorf("no answers: %v %v", m, err)
	}
}
