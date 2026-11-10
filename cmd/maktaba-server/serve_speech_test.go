package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

func fakeSpeechHardware(t *testing.T, m hostinfo.Machine) {
	t.Helper()
	prev, prevRun, prevM := speechHardware, runningSpeech, speechMeasurer
	t.Cleanup(func() { speechHardware, runningSpeech, speechMeasurer = prev, prevRun, prevM })
	speechHardware = func() hostinfo.Machine { return m }
	runningSpeech, speechMeasurer = nil, nil
}

func speechCall(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Epic 59 story 59.06: GET says the tier's speech (Light: off, base, why) and the last
// measure; PUT turns it on with base in one call, for the next start; a model the tier
// does not offer is refused; a measure with speech off is refused.
func TestConfigSpeechRoutes(t *testing.T) {
	if runningOnAppleSilicon() {
		t.Skip("Apple Silicon keeps MLX's model: the tiers' sizes are CTranslate2's")
	}
	fakeSpeechHardware(t, hostinfo.Machine{Arch: "arm64", Memory: hostinfo.Memory{LimitBytes: 4 << 30}})
	path := tomlWith(t, "tier = \"light\"\n")
	cfg, _, _ := config.LoadFrom(path)
	run := supervisor.SpeechOf(cfg, speechHardware())
	runningSpeech = &run
	h := (&configAdmin{path: path, token: "tok"}).adminHandler(http.NotFoundHandler())

	var doc speechDoc
	rec := speechCall(h, "GET", "/config/speech", "")
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil {
		t.Fatalf("GET %d %s", rec.Code, rec.Body)
	}
	if doc.Speech.On || doc.Speech.Model != "base" || doc.Speech.Reason != hostinfo.SpeechLight || doc.Measure != nil ||
		doc.Speech.SecondListen || doc.RestartNeeded {
		t.Fatalf("light: %+v", doc)
	}
	if rec := speechCall(h, "POST", "/config/speech/measure", ""); rec.Code != http.StatusConflict {
		t.Fatalf("measure with speech off: %d %s", rec.Code, rec.Body)
	}
	if rec := speechCall(h, "PUT", "/config/speech", `{"model":"small"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("small on Light: %d %s", rec.Code, rec.Body)
	}
	rec = speechCall(h, "PUT", "/config/speech", `{"speech":"on","model":"base"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &doc) != nil || !doc.Speech.On || !doc.RestartNeeded {
		t.Fatalf("turn on: %d %s", rec.Code, rec.Body)
	}
	saved, _, _ := config.LoadFrom(path)
	if saved.Transcription.Speech != "on" || saved.Transcription.Model != "base" {
		t.Fatalf("saved: %+v", saved.Transcription)
	}
	// a measure on disk is read back
	out := speechMeasureFile(saved)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, []byte(`{"model":"Systran/faster-whisper-base","size":"base","rtf":0.42,"busy":true,"clips":[{"lang":"ar","rtf":0.4,"wer":0.5}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = speechCall(h, "GET", "/config/speech", "")
	if json.Unmarshal(rec.Body.Bytes(), &doc) != nil || doc.Measure == nil || doc.Measure.RTF != 0.42 || !doc.Measure.Busy {
		t.Fatalf("measure: %s", rec.Body)
	}
	rec = speechCall(h, "PUT", "/config/speech", `{"speech":"auto","model":"auto"}`)
	saved, _, _ = config.LoadFrom(path)
	if rec.Code != 200 || saved.Transcription.Speech != "" || saved.Transcription.Model != "" {
		t.Fatalf("auto: %d %+v", rec.Code, saved.Transcription)
	}
	if rec := speechCall(h, "PUT", "/config/speech", `{"speech":"maybe"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("maybe: %d", rec.Code)
	}
}

// A measure is needed with speech on and none of its model yet; a failed one is tried again
// after six hours.
func TestSpeechStale(t *testing.T) {
	on := supervisor.Speech{On: true, Engine: "ct2", Repo: "Systran/faster-whisper-base"}
	now := time.Now()
	same := &speechMeasure{Model: on.Repo, RTF: 2}
	failed := &speechMeasure{Model: on.Repo, Error: "no engine"}
	for _, c := range []struct {
		s    supervisor.Speech
		m    *speechMeasure
		at   time.Time
		want bool
	}{
		{on, nil, now, true},
		{on, same, now, false},
		{on, &speechMeasure{Model: "Systran/faster-whisper-small"}, now, true},
		{on, failed, now.Add(-time.Hour), false},
		{on, failed, now.Add(-7 * time.Hour), true},
		{supervisor.Speech{On: false, Engine: "ct2", Repo: on.Repo}, nil, now, false},
		{supervisor.Speech{On: true, Engine: "mlx"}, nil, now, false},
	} {
		if got := speechStale(c.s, c.m, c.at, now); got != c.want {
			t.Errorf("%+v %+v: %v", c.s, c.m, got)
		}
	}
}

func runningOnAppleSilicon() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// A measure that ended with no answer of its own (a pipeline without the speech engine that
// could not even say so) is not started again for six hours.
func TestSpeechBackoff(t *testing.T) {
	r := &speechRunner{}
	now := time.Now()
	if r.backingOff("Systran/faster-whisper-base", now) {
		t.Fatal("nothing failed yet")
	}
	r.failedRepo, r.failedAt = "Systran/faster-whisper-base", now.Add(-time.Hour)
	if !r.backingOff("Systran/faster-whisper-base", now) || r.backingOff("Systran/faster-whisper-small", now) {
		t.Fatal("an hour after a failure of base")
	}
	if r.backingOff("Systran/faster-whisper-base", now.Add(6*time.Hour)) {
		t.Fatal("seven hours after")
	}
}
