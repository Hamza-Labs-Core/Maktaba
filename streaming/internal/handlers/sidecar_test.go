package handlers

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func opener(files map[string]string) *memoryOpener {
	m := &memoryOpener{files: map[string]*memoryFile{}}
	for path, body := range files {
		m.files[path] = &memoryFile{data: []byte(body), mt: time.Unix(0, 0)}
	}
	return m
}

const sampleSRT = "1\n00:00:01,000 --> 00:00:02,000\nHello & welcome\n\n"

func TestReadSidecar_VTTServedVerbatim(t *testing.T) {
	o := opener(map[string]string{
		"/media/movie.en.vtt": "WEBVTT\n\n1\n00:00:01.000 --> 00:00:02.000\nHi\n\n",
	})
	out, err := ReadSidecar("/media/movie.mkv", "en", o)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.HasPrefix(string(out), "WEBVTT") || !strings.Contains(string(out), "Hi") {
		t.Fatalf("vtt not served verbatim: %q", out)
	}
}

func TestReadSidecar_SRTConverted(t *testing.T) {
	o := opener(map[string]string{
		"/media/movie.en.srt": sampleSRT,
	})
	out, err := ReadSidecar("/media/movie.mkv", "en", o)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	s := string(out)
	if !strings.HasPrefix(s, "WEBVTT") {
		t.Fatalf("expected VTT header, got %q", s)
	}
	// SRT comma timecodes become VTT dots.
	if !strings.Contains(s, "00:00:01.000 --> 00:00:02.000") {
		t.Fatalf("timecodes not converted: %q", s)
	}
	// Cue text is HTML-escaped (AC-1): the ampersand must be entity-encoded.
	if !strings.Contains(s, "Hello &amp; welcome") {
		t.Fatalf("cue text not escaped: %q", s)
	}
}

func TestReadSidecar_BareLangName(t *testing.T) {
	// No movie-prefixed file; a bare ``ar.srt`` next to the video works.
	o := opener(map[string]string{
		"/media/ar.srt": sampleSRT,
	})
	out, err := ReadSidecar("/media/movie.mkv", "ar", o)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.HasPrefix(string(out), "WEBVTT") {
		t.Fatalf("expected VTT: %q", out)
	}
}

func TestReadSidecar_VTTPreferredOverSRT(t *testing.T) {
	// Both exist; the exact .vtt wins (no conversion round-trip).
	o := opener(map[string]string{
		"/media/movie.en.vtt": "WEBVTT\n\nNOTE exact\n",
		"/media/movie.en.srt": sampleSRT,
	})
	out, err := ReadSidecar("/media/movie.mkv", "en", o)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !strings.Contains(string(out), "NOTE exact") {
		t.Fatalf("expected the .vtt to win, got %q", out)
	}
}

func TestReadSidecar_NotFound(t *testing.T) {
	o := opener(map[string]string{"/media/movie.fr.srt": sampleSRT})
	_, err := ReadSidecar("/media/movie.mkv", "en", o)
	if !errors.Is(err, ErrSidecarNotFound) {
		t.Fatalf("expected ErrSidecarNotFound, got %v", err)
	}
}

func TestReadSidecar_RejectsPathTraversal(t *testing.T) {
	// A crafted lang must not escape the media directory.
	o := opener(map[string]string{"/etc/passwd": "secret"})
	for _, lang := range []string{"../../etc/passwd", "..", "a/b", `a\b`, ""} {
		if _, err := ReadSidecar("/media/movie.mkv", lang, o); !errors.Is(err, ErrSidecarNotFound) {
			t.Fatalf("lang %q: expected ErrSidecarNotFound, got %v", lang, err)
		}
	}
}
