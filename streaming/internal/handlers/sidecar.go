package handlers

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
)

// ErrSidecarNotFound is returned by ReadSidecar when no candidate
// sidecar file exists for the requested language.
var ErrSidecarNotFound = errors.New("sidecar subtitle not found")

// ReadSidecar locates a sidecar subtitle for videoPath in the given
// language and returns it as VTT bytes. Sidecars live in the same
// directory as the source media; we accept both the language-suffixed
// convention players use (“movie.en.srt“) and a bare “en.srt“ next
// to the file. “.vtt“ is served verbatim; “.srt“ is converted via
// SrtToVtt (which HTML-escapes cue text, AC-1).
//
// The candidate order prefers VTT (no conversion, exact) over SRT, and
// the movie-prefixed name over the bare-lang name (the former is what
// scrapers and ripping tools emit). lang is sanitised to a single path
// segment so a crafted “../“ can't escape the media directory.
func ReadSidecar(videoPath, lang string, open FileOpener) ([]byte, error) {
	lang = filepath.Base(strings.TrimSpace(lang))
	if lang == "" || lang == "." || lang == ".." || strings.ContainsAny(lang, `/\`) {
		return nil, ErrSidecarNotFound
	}

	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))

	candidates := []struct {
		name string
		srt  bool
	}{
		{base + "." + lang + ".vtt", false},
		{lang + ".vtt", false},
		{base + "." + lang + ".srt", true},
		{lang + ".srt", true},
	}

	for _, c := range candidates {
		body, err := readFile(filepath.Join(dir, c.name), open)
		if err != nil {
			continue
		}
		if !c.srt {
			return body, nil
		}
		return SrtToVtt(strings.NewReader(string(body)))
	}
	return nil, ErrSidecarNotFound
}

// readFile slurps a file through the FileOpener seam so tests don't
// need a real filesystem. Sidecars are small (a few hundred KB at
// most), so buffering whole is fine.
func readFile(path string, open FileOpener) ([]byte, error) {
	f, err := open.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}
