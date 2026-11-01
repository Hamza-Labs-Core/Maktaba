package browse_test

// The Android client's browse fixtures (story 48.8) are the answers its tests replay. This test
// decodes each one into the handlers' own response types and refuses any field they do not
// have, so a fixture cannot invent a field, misspell one or keep one the API dropped: renaming a
// JSON field here breaks this test, not the Player on a phone.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/browse"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/recommendations"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/search"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/series"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/videos"
)

// fixtureDir is the Android client's test resources, from this package's folder.
var fixtureDir = filepath.Join("..", "..", "..", "..", "apps", "android", "client", "src", "test", "resources", "browse")

// The handlers that build their answer from a map: the same keys, typed.
type (
	homeDoc struct {
		ContinueWatching     []browse.Card       `json:"continue_watching"`
		RecentlyAdded        []browse.Card       `json:"recently_added"`
		RecentlyWatched      []browse.Card       `json:"recently_watched"`
		Pinned               []browse.PinnedRows `json:"pinned"`
		OnDeck               []browse.Card       `json:"on_deck"`
		Favorites            []browse.Card       `json:"favorites"`
		RecentlyAddedGrouped []browse.AddedEntry `json:"recently_added_grouped"`
		Featured             *browse.Featured    `json:"featured"`
	}
	hubDoc struct {
		Library browse.LibraryRef `json:"library"`
		Rows    struct {
			ContinueWatching     []browse.Card       `json:"continue_watching"`
			RecentlyAdded        []browse.Card       `json:"recently_added"`
			RecentlyWatched      []browse.Card       `json:"recently_watched"`
			MostWatched          []browse.Card       `json:"most_watched"`
			Series               []browse.SeriesRef  `json:"series"`
			Years                []browse.YearCount  `json:"years"`
			OnDeck               []browse.Card       `json:"on_deck"`
			RecentlyAddedGrouped []browse.AddedEntry `json:"recently_added_grouped"`
		} `json:"rows"`
	}
	summaryDoc struct {
		Items []browse.LibrarySummary `json:"items"`
	}
	pinsDoc struct {
		LibraryIDs []string               `json:"library_ids"`
		Items      []browse.PinnedLibrary `json:"items"`
	}
	librarySeriesDoc struct {
		Library browse.LibraryRef          `json:"library"`
		Sort    string                     `json:"sort"`
		Items   []browse.LibrarySeriesItem `json:"items"`
		Total   int                        `json:"total"`
		Next    *int                       `json:"next"`
	}
	facetsDoc struct {
		Languages   []browse.LanguageFacet `json:"languages"`
		Tags        []browse.TagFacet      `json:"tags"`
		Speakers    []browse.SpeakerFacet  `json:"speakers"`
		Letters     []browse.LetterFacet   `json:"letters"`
		Durations   []browse.BandFacet     `json:"durations"`
		Added       []browse.BandFacet     `json:"added"`
		Resolutions []browse.BandFacet     `json:"resolutions"`
	}
	videosDoc struct {
		Items []videos.Video `json:"items"`
		Next  *string        `json:"next"`
	}
	chaptersDoc struct {
		Items []videos.Chapter `json:"items"`
	}
	segmentsDoc struct {
		Items   []videos.Segment `json:"items"`
		Partial bool             `json:"partial"`
	}
	seriesListDoc struct {
		Items []series.Item `json:"items"`
	}
	episodesDoc struct {
		Seasons     []series.Season `json:"seasons"`
		Numbering   string          `json:"numbering"`
		Sort        string          `json:"sort"`
		NextEpisode *series.Episode `json:"next_episode"`
	}
	// search.ResultsPage with its items typed by list
	titlesPage struct {
		search.ResultsPage
		Items []search.TitleVideo `json:"items"`
	}
	transcriptsPage struct {
		search.ResultsPage
		Items []search.TranscriptVideo `json:"items"`
	}
	recentDoc struct {
		Items []search.RecentSearch `json:"items"`
	}
	suggestDoc struct {
		Suggestions []string `json:"suggestions"`
	}
	updatedDoc struct {
		Updated int `json:"updated"`
	}
)

func TestAndroidBrowseFixturesMatchTheHandlers(t *testing.T) {
	cases := map[string]any{
		"home.json":               &homeDoc{},
		"recommendations.json":    &recommendations.Response{},
		"libraries-summary.json":  &summaryDoc{},
		"pinned-libraries.json":   &pinsDoc{},
		"hub.json":                &hubDoc{},
		"library-series.json":     &librarySeriesDoc{},
		"facets.json":             &facetsDoc{},
		"videos.json":             &videosDoc{},
		"video.json":              &videos.Video{},
		"my-video.json":           &browse.MyVideo{},
		"chapters.json":           &chaptersDoc{},
		"segments.json":           &segmentsDoc{},
		"series-list.json":        &seriesListDoc{},
		"series.json":             &series.Item{},
		"episodes.json":           &episodesDoc{},
		"search.json":             &search.Response{},
		"search-titles.json":      &titlesPage{},
		"search-transcripts.json": &transcriptsPage{},
		"search-recent.json":      &recentDoc{},
		"search-suggest.json":     &suggestDoc{},
		"updated.json":            &updatedDoc{},
	}
	files, err := filepath.Glob(filepath.Join(fixtureDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures in %s: %v", fixtureDir, err)
	}
	for _, f := range files {
		name := filepath.Base(f)
		into, ok := cases[name]
		if !ok {
			t.Errorf("%s: no handler type checks this fixture; add it to cases", name)
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
		delete(cases, name)
	}
	for name := range cases {
		t.Errorf("%s: fixture missing", name)
	}
}
