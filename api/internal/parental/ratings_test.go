package parental

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func sharedFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "shared", "parental", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The API serves and reads a copy of the shared file: the two are the same bytes.
func TestEmbeddedRatingsAreTheSharedFile(t *testing.T) {
	if !bytes.Equal(RatingsJSON(), sharedFile(t, "ratings.json")) {
		t.Fatal("api/internal/parental/ratings.json differs from shared/parental/ratings.json: copy the shared file over it")
	}
}

type vector struct {
	Input   string `json:"input"`
	Content *int   `json:"content"`
	Limit   *int   `json:"limit"`
}

func show(n int, ok bool) string {
	if !ok {
		return "none"
	}
	b, _ := json.Marshal(n)
	return string(b)
}

// T01: every label of every system maps to its tier, as content and as a limit.
func TestRatingVectors(t *testing.T) {
	var file struct {
		Vectors []vector `json:"vectors"`
	}
	if err := json.Unmarshal(sharedFile(t, "vectors.json"), &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 60 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	tab, err := NewTable(RatingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	tab.Logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	seen := map[string]bool{}
	for _, v := range file.Vectors {
		seen[v.Input] = true
		c, cok := tab.Content(v.Input)
		if want := show(deref(v.Content), v.Content != nil); show(c, cok) != want {
			t.Errorf("%q as content: %s, want %s", v.Input, show(c, cok), want)
		}
		l, lok := tab.Limit(v.Input)
		if want := show(deref(v.Limit), v.Limit != nil); show(l, lok) != want {
			t.Errorf("%q as a limit: %s, want %s", v.Input, show(l, lok), want)
		}
	}
	// the vectors name every label of every table
	var d tablesDef
	if err := json.Unmarshal(RatingsJSON(), &d); err != nil {
		t.Fatal(err)
	}
	for key, s := range d.Systems {
		for label := range s.Ages {
			if !seen[key+"/"+label] {
				t.Errorf("no vector for %s/%s", key, label)
			}
		}
		for label := range s.Scale {
			if !seen[key+"/"+label] {
				t.Errorf("no vector for %s/%s", key, label)
			}
		}
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func TestRounding(t *testing.T) {
	for _, c := range []struct{ age, content, limit int }{
		{0, 0, 0}, {1, 9, 0}, {8, 9, 0}, {9, 9, 9}, {12, 13, 9}, {13, 13, 13}, {15, 16, 13},
		{16, 16, 16}, {17, 18, 16}, {18, 18, 18}, {21, 18, 18},
	} {
		if got := ContentTier(c.age); got != c.content {
			t.Errorf("ContentTier(%d) = %d, want %d", c.age, got, c.content)
		}
		if got := LimitTier(c.age); got != c.limit {
			t.Errorf("LimitTier(%d) = %d, want %d", c.age, got, c.limit)
		}
		// content never rounds below a limit of the same age: a mapping errs toward hiding
		if ContentTier(c.age) < LimitTier(c.age) {
			t.Errorf("age %d: content below the limit", c.age)
		}
	}
	for _, n := range []int{0, 9, 13, 16, 18} {
		if !ValidTier(n) {
			t.Errorf("ValidTier(%d) = false", n)
		}
	}
	for _, n := range []int{-1, 1, 12, 17, 19, 1000} {
		if ValidTier(n) {
			t.Errorf("ValidTier(%d) = true", n)
		}
	}
}

// countHandler counts the log lines of one event.
type countHandler struct {
	mu    sync.Mutex
	lines []string
}

func (h *countHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *countHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "event" {
			h.mu.Lock()
			h.lines = append(h.lines, a.Value.String())
			h.mu.Unlock()
		}
		return true
	})
	return nil
}
func (h *countHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *countHandler) WithGroup(string) slog.Handler      { return h }

// T07: an unknown label is unrated, and is logged once however often it is read.
func TestUnknownRatingIsLoggedOnce(t *testing.T) {
	tab, err := NewTable(RatingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	h := &countHandler{}
	tab.Logger = slog.New(h)
	for i := 0; i < 2; i++ {
		if _, ok := tab.Content("XYZ"); ok {
			t.Fatal("XYZ is rated")
		}
	}
	if _, ok := tab.Limit(" xyz "); ok {
		t.Fatal("xyz is a limit")
	}
	if len(h.lines) != 1 || h.lines[0] != "parental.rating_unknown" {
		t.Fatalf("log lines %v, want one parental.rating_unknown", h.lines)
	}
	// another string is another line; an empty or a not-rated string is none
	tab.Content("Approved")
	tab.Content("")
	tab.Content("Not Rated")
	tab.Content("PG")
	if len(h.lines) != 2 {
		t.Fatalf("log lines %v, want two", h.lines)
	}
}

func TestUnknownRatingsRememberedAreBounded(t *testing.T) {
	tab, err := NewTable(RatingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	h := &countHandler{}
	tab.Logger = slog.New(h)
	for i := 0; i < maxUnknown+50; i++ {
		tab.Content("zz" + strings.Repeat("x", i%7) + show(i, true))
	}
	if len(h.lines) != maxUnknown {
		t.Fatalf("%d lines, want %d", len(h.lines), maxUnknown)
	}
}

func TestNewTableRefusesAnUndefinedSystem(t *testing.T) {
	if _, err := NewTable([]byte(`{"bare":["nope"],"systems":{}}`)); err == nil {
		t.Fatal("no error")
	}
	if _, err := NewTable([]byte(`{`)); err == nil {
		t.Fatal("no error for bad JSON")
	}
}
