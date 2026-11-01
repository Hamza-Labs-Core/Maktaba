package browse_test

// The native apps' browse fixtures (Epic 48, story 48.6) were recorded from this API on a
// scratch database. This test reads each one into the handlers' own types with unknown
// fields refused, so a renamed or removed JSON field fails here, in the PR that changes
// it, instead of silently drifting from what the Apple and Android clients are tested
// against. (A field the API adds is fine: the clients read leniently.) Re-record the
// fixtures in apps/apple/MaktabaKit/Tests/Fixtures/Browse when this fails on purpose.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/browse"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/series"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
)

const fixtureDir = "../../../../apps/apple/MaktabaKit/Tests/Fixtures/Browse"

func decodeStrict(t *testing.T, name string, into any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(fixtureDir, name+".json"))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(into); err != nil {
		t.Errorf("fixture %s no longer matches the API's types: %v", name, err)
	}
}

type homeRows struct {
	Featured             *browse.Featured    `json:"featured"`
	ContinueWatching     []browse.Card       `json:"continue_watching"`
	OnDeck               []browse.Card       `json:"on_deck"`
	Favorites            []browse.Card       `json:"favorites"`
	RecentlyAdded        []browse.Card       `json:"recently_added"`
	RecentlyAddedGrouped []browse.AddedEntry `json:"recently_added_grouped"`
	RecentlyWatched      []browse.Card       `json:"recently_watched"`
	Pinned               []browse.PinnedRows `json:"pinned"`
}

type hub struct {
	Library browse.LibraryRef `json:"library"`
	Rows    struct {
		ContinueWatching     []browse.Card       `json:"continue_watching"`
		OnDeck               []browse.Card       `json:"on_deck"`
		RecentlyAdded        []browse.Card       `json:"recently_added"`
		RecentlyAddedGrouped []browse.AddedEntry `json:"recently_added_grouped"`
		RecentlyWatched      []browse.Card       `json:"recently_watched"`
		MostWatched          []browse.Card       `json:"most_watched"`
		Series               []browse.SeriesRef  `json:"series"`
		Years                []browse.YearCount  `json:"years"`
	} `json:"rows"`
}

type items[T any] struct {
	Items []T `json:"items"`
}

func TestNativeBrowseFixturesMatchTheAPI(t *testing.T) {
	decodeStrict(t, "home", &homeRows{})
	decodeStrict(t, "library-hub", &hub{})
	decodeStrict(t, "libraries-summary", &items[browse.LibrarySummary]{})
	var pins struct {
		LibraryIDs []string               `json:"library_ids"`
		Items      []browse.PinnedLibrary `json:"items"`
	}
	decodeStrict(t, "pinned-libraries", &pins)
	decodeStrict(t, "pinned-libraries-put", &pins)
	var facets struct {
		Languages   []browse.LanguageFacet `json:"languages"`
		Tags        []browse.TagFacet      `json:"tags"`
		Speakers    []browse.SpeakerFacet  `json:"speakers"`
		Letters     []browse.LetterFacet   `json:"letters"`
		Durations   []browse.BandFacet     `json:"durations"`
		Added       []browse.BandFacet     `json:"added"`
		Resolutions []browse.BandFacet     `json:"resolutions"`
	}
	decodeStrict(t, "videos-facets", &facets)
	var libSeries struct {
		Library browse.LibraryRef          `json:"library"`
		Items   []browse.LibrarySeriesItem `json:"items"`
		Next    *int                       `json:"next"`
		Sort    string                     `json:"sort"`
		Total   int                        `json:"total"`
	}
	decodeStrict(t, "library-series", &libSeries)
	decodeStrict(t, "my-video", &browse.MyVideo{})
	var marks struct {
		Updated int `json:"updated"`
	}
	decodeStrict(t, "mark-watched", &marks)

	decodeStrict(t, "series-list", &items[series.Item]{})
	decodeStrict(t, "series", &series.Item{})
	var episodes struct {
		Numbering   string          `json:"numbering"`
		Sort        string          `json:"sort"`
		Seasons     []series.Season `json:"seasons"`
		NextEpisode *series.Episode `json:"next_episode"`
	}
	decodeStrict(t, "series-episodes", &episodes)

	decodeStrict(t, "video-chapters", &items[videos.Chapter]{})
	decodeStrict(t, "video-subtitles", &items[videos.Subtitle]{})
	var segments struct {
		Items   []videos.Segment `json:"items"`
		Partial bool             `json:"partial"`
	}
	decodeStrict(t, "video-segments", &segments)
}
