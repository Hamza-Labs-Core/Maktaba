package graphql

// GraphQL object types. Most are the REST wire shape decoded as is (json
// tags = REST field names; graph-gophers resolves the schema field by
// struct field name). Video and Transcript carry their batch loader and
// resolve through methods (loaders.go).

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	gql "github.com/graph-gophers/graphql-go"
)

type library struct {
	ID        ID       `json:"id"`
	Name      string   `json:"name"`
	Roots     []string `json:"roots"`
	Settings  *JSON    `json:"settings"`
	CreatedAt gql.Time `json:"created_at"`
	UpdatedAt gql.Time `json:"updated_at"`
}

type job struct {
	ID              ID        `json:"id"`
	VideoID         *ID       `json:"video_id"`
	LibraryID       *ID       `json:"library_id"`
	Stage           string    `json:"stage"`
	State           string    `json:"state"`
	Priority        int32     `json:"priority"`
	Attempts        int32     `json:"attempts"`
	PauseRequested  bool      `json:"pause_requested"`
	CancelRequested bool      `json:"cancel_requested"`
	CreatedAt       gql.Time  `json:"created_at"`
	NotBefore       *gql.Time `json:"not_before"`
	Error           *string   `json:"error"`
}

type jobConnection struct {
	Items      []*job
	NextCursor *string
}

type tag struct {
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

type speaker struct {
	ID           ID      `json:"id"`
	Name         string  `json:"name"`
	ClusterLabel *string `json:"cluster_label"`
}

type device struct {
	ID           ID       `json:"id"`
	Platform     string   `json:"platform"`
	BundleID     string   `json:"bundle_id"`
	AppVersion   *string  `json:"app_version"`
	Locale       *string  `json:"locale"`
	RegisteredAt gql.Time `json:"registered_at"`
	LastSeenAt   gql.Time `json:"last_seen_at"`
}

type streamingSession struct {
	ID          ID        `json:"session_id"`
	VideoID     ID        `json:"video_id"`
	Mode        string    `json:"mode"`
	ManifestURL *string   `json:"manifest_url"`
	DirectURL   *string   `json:"direct_url"`
	ExpiresAt   *gql.Time `json:"expires_at"`
	OpenedAt    gql.Time  `json:"opened_at"`
	LastSeenAt  *gql.Time `json:"last_segment_fetched_at"`
	ClosedAt    *gql.Time `json:"closed_at"`
}

type playbackState struct {
	PositionSec float64
	Completed   bool
	UpdatedAt   gql.Time
}

type mediaInfo struct {
	Container   *string
	DurationSec *float64
	Width       *int32
	Height      *int32
	FPS         *float64
	BitrateKbps *int32
}

type audioTrack struct {
	ID       int32
	Language *string
	Codec    string
	Channels *int32
	Default  bool
}

type subtitle struct {
	ID        ID
	Language  string
	Format    string
	Source    string
	IsDefault bool
	URL       string
}

type chapter struct {
	Seq      int32
	StartSec float64
	EndSec   *float64
	Title    string
	Source   string
}

type word struct {
	Seq        int32    `json:"seq"`
	StartSec   float64  `json:"start_sec"`
	EndSec     float64  `json:"end_sec"`
	Text       string   `json:"text"`
	Confidence *float64 `json:"confidence"`
}

type segment struct {
	ID       ID       `json:"id"`
	Seq      int32    `json:"seq"`
	StartSec float64  `json:"start_sec"`
	EndSec   float64  `json:"end_sec"`
	Text     string   `json:"text"`
	Speaker  *string  `json:"speaker"`
	Words    *[]*word `json:"words"`
}

type searchMatch struct {
	SegmentID ID      `json:"segment_id"`
	VideoID   ID      `json:"video_id"`
	StartSec  float64 `json:"start_sec"`
	EndSec    float64 `json:"end_sec"`
	Snippet   string  `json:"snippet"`
	Score     float64 `json:"score"`
}

type searchHit struct {
	VideoID   ID
	BestMatch *searchMatch
	Matches   []*searchMatch
}

type searchResult struct {
	Hits   []*searchHit
	Total  int32
	TookMs JSON
}

type railCard struct {
	ID           ID
	Title        string
	DurationSec  float64
	PositionSec  *float64
	RemainingSec *float64
	PosterURL    *string
	Snippet      *string
	Reason       *string
}

type deleteResult struct {
	Deleted     bool
	FailedPaths []string
	Warning     *string
}

// collection is a REST collection; items resolve through the REST items
// route and the shared video loader.
type collection struct {
	ID          ID      `json:"id"`
	LibraryID   ID      `json:"library_id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	IsSmart     bool    `json:"is_smart"`
	SmartQuery  *JSON   `json:"smart_query"`

	rs *Resolvers
}

// Items resolves GET /api/collections/{id}/items, then the videos by id
// (one batch), keeping the collection's order and dropping videos the
// caller may not read.
func (c *collection) Items(ctx context.Context) ([]*videoNode, error) {
	var page struct {
		Items []struct {
			VideoID string `json:"video_id"`
		} `json:"items"`
	}
	if _, err := c.rs.Bridge.json(ctx, call{method: http.MethodGet, path: "/api/collections/" + url.PathEscape(string(c.ID)) + "/items"}, &page); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Items))
	for _, it := range page.Items {
		ids = append(ids, it.VideoID)
	}
	return c.rs.videosByID(ctx, ids)
}

func i32(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
