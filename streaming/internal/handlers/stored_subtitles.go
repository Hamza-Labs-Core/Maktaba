package handlers

import (
	"context"
	"slices"
	"strings"
)

// StoredSubtitles reads the subtitles kept in the database for a video whose library is
// read-only for Maktaba (Epic 59 story 59.03, subtitle_files.content): the pipeline could not
// write them beside the video, so the row is the file. They are served like side-car files,
// after them: a side-car beside the video wins for its language.
type StoredSubtitles interface {
	// Langs are the languages the database holds a subtitle in for videoID.
	Langs(ctx context.Context, videoID string) ([]string, error)
	// VTT is the subtitle in lang as WebVTT; ErrSidecarNotFound when there is none.
	VTT(ctx context.Context, videoID, lang string) ([]byte, error)
}

// ReadSubtitle is ReadSidecar, then the database's copy (stored may be nil): the side-car
// beside the video first, the row after it.
func ReadSubtitle(ctx context.Context, videoPath, videoID, lang string, open FileOpener, stored StoredSubtitles) ([]byte, error) {
	body, err := ReadSidecar(videoPath, lang, open)
	if err == nil || stored == nil || videoID == "" {
		return body, err
	}
	lang = strings.TrimSpace(lang)
	if !subtitleLang.MatchString(lang) {
		return nil, ErrSidecarNotFound
	}
	return stored.VTT(ctx, videoID, lang)
}

// MergeLangs is the side-cars' languages and the database's, sorted, each once (only the
// ones the renditions' paths can carry).
func MergeLangs(sidecars, stored []string) []string {
	out := slices.Clone(sidecars)
	for _, l := range stored {
		if subtitleLang.MatchString(l) && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	slices.Sort(out)
	return out
}
