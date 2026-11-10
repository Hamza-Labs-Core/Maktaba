package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type fakeStored map[string]string // lang -> vtt

func (f fakeStored) Langs(context.Context, string) ([]string, error) {
	var out []string
	for l := range f {
		out = append(out, l)
	}
	return out, nil
}

func (f fakeStored) VTT(_ context.Context, _, lang string) ([]byte, error) {
	if v, ok := f[lang]; ok {
		return []byte(v), nil
	}
	return nil, ErrSidecarNotFound
}

// T05 (story 59.03): with both a side-car and a database row, the side-car is served; with
// only the row, the row.
func TestReadSubtitleSidecarFirstThenRow(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "Talk.mp4")
	if err := os.WriteFile(filepath.Join(dir, "Talk.en.vtt"), []byte("WEBVTT\n\nside-car"), 0o644); err != nil {
		t.Fatal(err)
	}
	stored := fakeStored{"en": "WEBVTT\n\nrow en", "ar": "WEBVTT\n\nrow ar"}
	ctx := context.Background()
	got, err := ReadSubtitle(ctx, video, "vid", "en", OSFileOpener{}, stored)
	if err != nil || string(got) != "WEBVTT\n\nside-car" {
		t.Fatalf("en: %q %v", got, err)
	}
	got, err = ReadSubtitle(ctx, video, "vid", "ar", OSFileOpener{}, stored)
	if err != nil || string(got) != "WEBVTT\n\nrow ar" {
		t.Fatalf("ar: %q %v", got, err)
	}
	if _, err := ReadSubtitle(ctx, video, "vid", "fr", OSFileOpener{}, stored); !errors.Is(err, ErrSidecarNotFound) {
		t.Fatalf("fr: %v", err)
	}
	if _, err := ReadSubtitle(ctx, video, "vid", "../x", OSFileOpener{}, stored); !errors.Is(err, ErrSidecarNotFound) {
		t.Fatalf("a crafted language: %v", err)
	}
	if _, err := ReadSubtitle(ctx, video, "vid", "ar", OSFileOpener{}, nil); !errors.Is(err, ErrSidecarNotFound) {
		t.Fatalf("no database: %v", err)
	}
}

func TestMergeLangs(t *testing.T) {
	got := MergeLangs([]string{"en"}, []string{"ar", "en", "bad lang"})
	if !slices.Equal(got, []string{"ar", "en"}) {
		t.Fatalf("%v", got)
	}
}
