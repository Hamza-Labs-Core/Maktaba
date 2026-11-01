package browse_test

// The Android client's Profile fixtures (story 48.31) are the answers ProfileApiTest replays.
// As TestAndroidBrowseFixturesMatchTheHandlers does for browse, this decodes each one into the
// handlers' own response types and refuses any field they do not have. The sessions fixture is
// checked in handlers/auth, whose answer type is unexported there.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/browse"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/watch"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/system"
)

var profileFixtureDir = filepath.Join("..", "..", "..", "..", "apps", "android", "client", "src", "test", "resources", "profile")

type (
	historyDoc struct {
		Items  []watch.HistoryItem `json:"items"`
		Limit  int                 `json:"limit"`
		Offset int                 `json:"offset"`
	}
	playlistsDoc struct {
		Items []browse.PlaylistSummary `json:"items"`
	}
	playlistDoc struct {
		Playlist browse.PlaylistSummary `json:"playlist"`
		Items    []browse.Card          `json:"items"`
	}
)

func TestAndroidProfileFixturesMatchTheHandlers(t *testing.T) {
	cases := map[string]any{
		"history.json":   &historyDoc{},
		"playlists.json": &playlistsDoc{},
		"playlist.json":  &playlistDoc{},
		"favorites.json": &videosDoc{},
		"version.json":   &system.VersionInfo{},
		// checked in handlers/auth (TestAndroidSessionsFixtureMatchesTheHandler)
		"sessions.json": nil,
	}
	files, err := filepath.Glob(filepath.Join(profileFixtureDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", profileFixtureDir, err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		into, ok := cases[name]
		if !ok {
			t.Errorf("%s: no handler type checks this fixture; add it to cases", name)
			continue
		}
		delete(cases, name)
		if into == nil {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(into); err != nil {
			t.Errorf("%s does not decode as the handler's answer: %v", name, err)
		}
	}
	for name := range cases {
		t.Errorf("%s: fixture missing", name)
	}
}
