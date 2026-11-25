package videos

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/principal"
)

// A collection an older release read in from Plex is an ordinary one: in a
// library's collection mode its card shows with no source_kind.
func TestModeCollectionsPlexReadHasNoSource(t *testing.T) {
	db := extDB(t)
	for _, s := range []string{
		`ALTER TABLE videos ADD COLUMN deleted_at DATETIME`,
		`CREATE TABLE collections (id TEXT PRIMARY KEY, library_id TEXT, name TEXT, is_smart INTEGER NOT NULL DEFAULT 0,
			hand_ordered INTEGER NOT NULL DEFAULT 0, dismissed_at TEXT, source_kind TEXT, name_override TEXT,
			hidden_at TEXT, sort_title TEXT)`,
		`CREATE TABLE collection_items (collection_id TEXT, video_id TEXT, position INTEGER)`,
		`INSERT INTO collections (id, library_id, name, source_kind) VALUES
			('c1', '` + extLibA + `', 'Picks', NULL), ('px', '` + extLibA + `', 'Plex picks', 'plex'),
			('yt', '` + extLibA + `', 'Talks', 'youtube_channel')`,
		`INSERT INTO collection_items VALUES ('c1', '` + xvid(1) + `', 0), ('px', '` + xvid(2) + `', 0), ('yt', '` + xvid(3) + `', 0)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	cards, err := (&Handler{DB: db}).modeCollections(t.Context(), &principal.Principal{AccessAllLibraries: true}, extLibA)
	if err != nil {
		t.Fatal(err)
	}
	// a YouTube one takes no part in the mode
	if len(cards) != 2 || cards[0].ID != "c1" || cards[1].ID != "px" {
		t.Fatalf("cards %+v", cards)
	}
	for _, c := range cards {
		if c.SourceKind != "" {
			t.Errorf("%s has source %q", c.ID, c.SourceKind)
		}
	}
}
