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

// script is a terminal that answers from queues. Asked something it has no
// answer for, it ends its input (Ctrl-D) when eof is set, else fails the test.
type script struct {
	t       *testing.T
	lines   []string
	secrets []string
	yes     []bool
	eof     bool
}

func (s *script) none(kind, q string) error {
	if !s.eof {
		s.t.Fatalf("unexpected %s %q", kind, q)
	}
	return errCancelled
}

func (s *script) line(q, def string) (string, error) {
	if len(s.lines) == 0 {
		return "", s.none("question", q)
	}
	v := s.lines[0]
	s.lines = s.lines[1:]
	if v == "" {
		return def, nil
	}
	return v, nil
}

func (s *script) secret(q string) (string, error) {
	if len(s.secrets) == 0 {
		return "", s.none("secret question", q)
	}
	v := s.secrets[0]
	s.secrets = s.secrets[1:]
	return v, nil
}

func (s *script) yesNo(q string, _ bool) (bool, error) {
	if len(s.yes) == 0 {
		return false, s.none("yes/no", q)
	}
	v := s.yes[0]
	s.yes = s.yes[1:]
	return v, nil
}

var checkOK = map[string]any{"ok": true, "data_dir": "/home/me/.local/share/maktaba", "problems": []any{},
	"warnings": []map[string]any{{"code": "disk_tight", "message": "Less than 40 GB free",
		"params": map[string]any{"free_bytes": 30 << 30, "comfortable_bytes": 40 << 30}}},
	"runs": []map[string]any{{"role": "postgres", "what": "The library database"},
		{"role": "pipeline", "what": "Indexing", "params": map[string]any{"workers": 2, "gpu": "RTX 4090"}}}}

// interactive is a whole interactive run's script: admin (a mismatch, then the
// same twice), libraries (a folder the step refuses, then a path map without
// "=>", then a good one), models (a token), Plex linked.
func interactive(t *testing.T) *script {
	return &script{t: t,
		lines:   []string{"admin", "/media/x", "", "/media/y", "/mnt/media", "/mnt/media => /srv/media"},
		secrets: []string{"pw-one", "pw-two", "s3cret!", "s3cret!", "hf_token"},
		yes:     []bool{true}}
}

func interactiveSteps() *fakeSteps {
	return &fakeSteps{answers: map[string][]any{
		"status": {map[string]any{"configured": false}},
		"check":  {checkOK},
		"libraries": {&stepFailure{Code: "root_missing", Message: "/media/x is not a folder", Field: "roots",
			Params: map[string]any{"path": "/media/x"}}, map[string]any{}},
		"plex":   {map[string]any{"pin_id": 7, "code": "ABCD", "link_url": "https://plex.tv/link"}, map[string]any{"linked": false}, map[string]any{"linked": true}},
		"finish": {map[string]any{"web_url": "http://127.0.0.1:8088"}},
	}}
}

func TestAppSetupInteractive(t *testing.T) {
	f := interactiveSteps()
	var out bytes.Buffer
	finished := false
	a := &appSetup{run: f.run, ask: interactive(t), out: &out, t: textsFor("en"), pollEvery: time.Millisecond,
		runtime: func(context.Context) (stepRunner, error) {
			f.calls = append(f.calls, "[runtime]")
			return f.run, nil
		},
		afterFinish: func(context.Context) error { finished = true; return nil }}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !finished {
		t.Error("the service was not installed after finish")
	}
	// the runtime is in place before anything is built on it; then the
	// desktop's last screen: venv, the models' download, the language model, finish
	want := "status check [runtime] admin libraries libraries models plex plex plex venv models llm finish"
	if got := strings.Join(f.calls, " "); got != want {
		t.Errorf("steps %q, want %q", got, want)
	}
	if got := f.inputs["admin"][0]; got != `{"password":"s3cret!","username":"admin"}` {
		t.Errorf("admin input %s", got)
	}
	if got := f.inputs["libraries"][1]; got != `{"path_map":[{"from":"/mnt/media","to":"/srv/media"}],"roots":["/media/y"]}` {
		t.Errorf("libraries input after the error %s", got)
	}
	if got := f.inputs["models"]; len(got) != 2 || got[0] != `{"hf_token":"hf_token","source":"huggingface"}` || got[1] != `{"action":"download"}` {
		t.Errorf("models inputs %v", got)
	}
	if got := f.inputs["plex"][1]; got != `{"action":"poll","pin_id":7}` {
		t.Errorf("plex poll %s", got)
	}
	for _, w := range []string{"/media/x is not a folder on this computer.", "The two passwords are not the same.",
		`Write each line as "their path => the path here".`,
		"Note: Less than 40.0 GB free: the database, the models and the caches grow with the library.",
		"  - The library database (private to Maktaba)\n",
		"  - Indexing, transcription (2 workers) and search, transcribing on RTX 4090\n",
		"Enter this code at https://plex.tv/link: ABCD", "Plex is linked.", "Open http://127.0.0.1:8088",
		"working on venv (50%)"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	if strings.Contains(out.String(), "s3cret!") || strings.Contains(out.String(), "hf_token\"") {
		t.Error("a secret was printed")
	}
}

func TestAppSetupInArabic(t *testing.T) {
	var out bytes.Buffer
	a := &appSetup{run: interactiveSteps().run, ask: interactive(t), out: &out, t: textsFor("ar"), pollEvery: time.Millisecond}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	ar := textsFor("ar")
	note := ar.coded("setup.check.note", "disk_tight", map[string]any{"comfortable_bytes": float64(40 << 30)}, "")
	for _, w := range []string{"كلمتا المرور غير متطابقتين.", "تم ربط Plex.", "اكتمل إعداد مكتبة. افتح http://127.0.0.1:8088",
		"ملاحظة: " + note, "40.0 GB", ar.coded("setup.error", "root_missing", map[string]any{"path": "/media/x"}, "")} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
	if strings.Contains(note, "{") || !strings.Contains(ar.coded("setup.error", "root_missing", map[string]any{"path": "/media/x"}, ""), "/media/x") {
		t.Errorf("params not filled: %q", note)
	}
	// a step's error by its code, in Arabic; the step's message when the app has no text for it
	if got := ar.stepError(&stepFailure{Code: "hf_token_invalid", Message: "Hugging Face did not accept this token."}); got != "لم يقبل Hugging Face الرمز." {
		t.Errorf("hf_token_invalid in Arabic: %q", got)
	}
	if got := ar.stepError(&stepFailure{Code: "a_new_code", Message: "/x is odd"}); got != "/x is odd" {
		t.Errorf("an unknown code: %q", got)
	}
}

func TestCodedTexts(t *testing.T) {
	en := textsFor("en")
	if got := en.coded("setup.check.note", "disk_low", map[string]any{"free_bytes": float64(5 << 30), "need_bytes": float64(20 << 30),
		"dir": "/data"}, "english"); got != "Only 5.0 GB free on the disk of /data: Maktaba needs 20.0 GB to start." {
		t.Errorf("disk_low: %q", got)
	}
	if got := en.coded("setup.check.note", "ram_low", map[string]any{"have_bytes": float64(3 << 30), "need_bytes": float64(4 << 30)}, ""); !strings.Contains(got, "this computer") {
		t.Errorf("the platform's words: %q", got)
	}
	for _, c := range []struct {
		params map[string]any
		want   string
	}{
		{nil, "Indexing, transcription and search"},
		{map[string]any{"workers": float64(1)}, "Indexing, transcription (1 worker) and search"},
		{map[string]any{"workers": float64(3), "gpu": "RTX 3060"}, "Indexing, transcription (3 workers) and search, transcribing on RTX 3060"},
	} {
		if got := en.runText("pipeline", "english", c.params); got != c.want {
			t.Errorf("runText(%v) = %q", c.params, got)
		}
	}
	if got := en.runText("a_new_role", "what it does", nil); got != "what it does" {
		t.Errorf("an unknown role: %q", got)
	}
	for n, want := range map[float64]string{512: "512 B", 1536: "1.5 KB", 5 << 30: "5.0 GB"} {
		if got := formatBytes(n); got != want {
			t.Errorf("formatBytes(%v) = %q", n, got)
		}
	}
}

// Ctrl-D at a question ends the setup instead of asking forever.
func TestAppSetupEndOfInputCancels(t *testing.T) {
	f := interactiveSteps()
	sc := &script{t: t, lines: []string{"admin"}, eof: true}
	a := &appSetup{run: f.run, ask: sc, out: &bytes.Buffer{}, t: textsFor("en")}
	if err := a.Run(context.Background()); !errors.Is(err, errCancelled) {
		t.Fatalf("err = %v", err)
	}
	if strings.Join(f.calls, " ") != "status check" {
		t.Errorf("steps %v", f.calls)
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
	a := &appSetup{run: f.run, ask: &script{t: t}, out: &bytes.Buffer{}, t: textsFor("en"), answers: answers}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.inputs["admin"][0]; got != `{"username":"admin","password":"s3cret!"}` {
		t.Errorf("admin input %s", got)
	}
	if got := f.inputs["plex"][0]; got != `{"action":"skip"}` {
		t.Errorf("plex %s", got)
	}
	// the share's copy is named once; the download after the venv is the desktop's
	if got := f.inputs["models"]; len(got) != 2 || got[1] != `{"action":"download"}` {
		t.Errorf("models %v", got)
	}

	// an answered step that fails is not retried
	f = &fakeSteps{answers: map[string][]any{"check": {checkOK},
		"admin": {&stepFailure{Code: "password_short", Message: "too short", Field: "password", Params: map[string]any{"min": float64(8)}}}}}
	a = &appSetup{run: f.run, ask: &script{t: t}, out: &bytes.Buffer{}, t: textsFor("en"), answers: answers}
	err := a.Run(context.Background())
	var fail *stepFailure
	if !errors.As(err, &fail) || fail.Code != "password_short" {
		t.Errorf("err = %v", err)
	}
}

// The language model: only too little disk stops the setup; anything else is
// left to the server, which downloads it in the background (as the desktop).
func TestAppSetupLanguageModel(t *testing.T) {
	answers := map[string]json.RawMessage{
		"admin":     json.RawMessage(`{"username":"admin","password":"s3cret!"}`),
		"libraries": json.RawMessage(`{"roots":["/srv/media"],"path_map":[]}`),
		"models":    json.RawMessage(`{"source":"nas","path":"/srv/models"}`),
		"plex":      json.RawMessage(`{"action":"skip"}`),
	}
	for code, stops := range map[string]bool{"disk_full": true, "llm_unavailable": false} {
		f := &fakeSteps{answers: map[string][]any{"check": {checkOK},
			"llm": {&stepFailure{Code: code, Message: code}}}}
		var out bytes.Buffer
		a := &appSetup{run: f.run, ask: &script{t: t}, out: &out, t: textsFor("en"), answers: answers}
		err := a.Run(context.Background())
		if stops != (err != nil) {
			t.Errorf("%s: err = %v", code, err)
		}
		if !stops && (!strings.Contains(out.String(), "downloads it in the background") || !strings.HasSuffix(strings.Join(f.calls, " "), "llm finish")) {
			t.Errorf("%s: steps %v\n%s", code, f.calls, out.String())
		}
	}
}

func TestAppSetupStopsWhenTheComputerCannotRunIt(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{"check": {map[string]any{"ok": false, "warnings": []any{},
		"problems": []map[string]any{{"code": "glibc_old", "message": "glibc 2.31; Maktaba needs 2.35",
			"params": map[string]any{"have": "2.31", "need": "2.35"}}}}}}}
	var out bytes.Buffer
	ran := false
	a := &appSetup{run: f.run, ask: &script{t: t}, out: &out, t: textsFor("en"),
		runtime: func(context.Context) (stepRunner, error) { ran = true; return f.run, nil }}
	if err := a.Run(context.Background()); err == nil {
		t.Fatal("setup went on")
	}
	if !strings.Contains(out.String(), "cannot run its server on this computer:\n  - This Linux has glibc 2.31; Maktaba needs 2.35 or newer") {
		t.Errorf("output %s", out.String())
	}
	if strings.Join(f.calls, " ") != "status check" || ran {
		t.Errorf("steps %v, runtime copied %v", f.calls, ran)
	}
}

func TestAppSetupOnAConfiguredComputerReinstallsTheService(t *testing.T) {
	f := &fakeSteps{answers: map[string][]any{"status": {map[string]any{"configured": true, "web_url": "http://127.0.0.1:8088"}}}}
	var order []string
	a := &appSetup{run: f.run, ask: &script{t: t, yes: []bool{true}}, out: &bytes.Buffer{}, t: textsFor("en"),
		runtime:     func(context.Context) (stepRunner, error) { order = append(order, "runtime"); return f.run, nil },
		afterFinish: func(context.Context) error { order = append(order, "service"); return nil }}
	if err := a.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "runtime,service" || strings.Join(f.calls, " ") != "status" {
		t.Errorf("order %v, steps %v", order, f.calls)
	}
}

func TestAppSetupRefusesRoot(t *testing.T) {
	prevGOOS, prevEUID := runtimeGOOS, geteuid
	t.Cleanup(func() { runtimeGOOS, geteuid = prevGOOS, prevEUID })
	runtimeGOOS, geteuid = "linux", func() int { return 0 }
	if err := runAppSetupCmd("", 0, "en"); err == nil || !strings.Contains(err.Error(), "without sudo") {
		t.Errorf("as root: %v", err)
	}
	if err := runAppSetupCmd("", 0, "fr"); err == nil || !strings.Contains(err.Error(), "--lang") {
		t.Errorf("an unknown language: %v", err)
	}
}

func TestParsePathMap(t *testing.T) {
	if pm, ok := parsePathMap(" /a => /b , /c=>/d "); !ok || len(pm) != 2 || pm[1]["from"] != "/c" || pm[1]["to"] != "/d" {
		t.Errorf("good: %v %v", pm, ok)
	}
	if pm, ok := parsePathMap(""); !ok || len(pm) != 0 {
		t.Errorf("empty: %v %v", pm, ok)
	}
	for _, bad := range []string{"/a", "/a =>", "=> /b", "/a => /b, /c"} {
		if _, ok := parsePathMap(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestYesNoAnswer(t *testing.T) {
	for in, want := range map[string]bool{"y": true, "YES": true, "ن": true, "نعم": true, "n": false, "لا": false, "ل": false} {
		if got := yesNoAnswer(in, !want); got != want {
			t.Errorf("%q = %v", in, got)
		}
	}
	if !yesNoAnswer("", true) || yesNoAnswer("maybe", false) {
		t.Error("the default")
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
	_ = os.WriteFile(good, []byte(`{"admin":{"username":"a","password":"b"},"plex":{"action":"skip"},"llm":{}}`), 0o600)
	m, err := readAnswers(good, 0)
	if err != nil || len(m) != 3 {
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

// An expired Plex code (or Plex out of reach) is "link it later": the setup
// goes on and keeps every answer given before it.
func TestAppSetupLinksPlexLater(t *testing.T) {
	f := interactiveSteps()
	f.answers["plex"] = []any{map[string]any{"pin_id": 7, "code": "ABCD", "link_url": "https://plex.tv/link"},
		&stepFailure{Code: "pin_expired", Message: "the PIN expired"}, map[string]any{}}
	var out bytes.Buffer
	a := &appSetup{run: f.run, ask: interactive(t), out: &out, t: textsFor("en"), pollEvery: time.Millisecond}
	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := f.inputs["plex"]; len(got) != 3 || got[2] != `{"action":"skip"}` {
		t.Errorf("plex inputs %v", got)
	}
	if !strings.HasSuffix(strings.Join(f.calls, " "), "venv models llm finish") {
		t.Errorf("steps %v", f.calls)
	}
	for _, w := range []string{"The Plex code expired. Try again.", "link it later in Settings"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out.String())
		}
	}
}

func TestEndError(t *testing.T) {
	ar := textsFor("ar")
	if err := endError(ar, nil); err != nil {
		t.Errorf("nil: %v", err)
	}
	if err := endError(ar, wrapStep("venv", &stepFailure{Code: "venv_failed", Message: "pip failed"})); err == nil ||
		err.Error() != ar.get("setup.error.venv_failed") {
		t.Errorf("a step's failure: %v", err)
	}
	if err := endError(ar, errCancelled); err == nil || err.Error() != ar.get("terminal.cancelled") {
		t.Errorf("cancel: %v", err)
	}
	other := errors.New("exec: not found")
	if err := endError(ar, other); err != other {
		t.Errorf("another error: %v", err)
	}
}
