package broadcast

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/clustertest"
)

// pubHome is a scratch home with one channel and a few items: a YouTube video from
// channel UCxxxx (standard licence), one under CC BY, one from another channel, and a
// Quran recitation in a reciter's library.
type pubHome struct {
	db                          *sql.DB
	ch                          string
	permitted, cc, other, quran string
	lib, reciterLib             string
}

func newPubHome(t *testing.T) pubHome {
	t.Helper()
	db, _ := clustertest.ScratchDB(t)
	h := pubHome{db: db}
	q := func(dst *string, query string, args ...any) {
		t.Helper()
		if err := db.QueryRow(query, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	x := func(query string, args ...any) {
		t.Helper()
		if _, err := db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	q(&h.ch, `INSERT INTO channels (number, name, slug, mode) VALUES (7, 'Lessons', 'lessons', 'shuffle') RETURNING id::text`)
	q(&h.lib, `INSERT INTO libraries (name, roots) VALUES ('Lessons', '{}') RETURNING id::text`)
	q(&h.reciterLib, `INSERT INTO libraries (name, roots) VALUES ('Reciter A', '{}') RETURNING id::text`)
	video := func(dst *string, lib, name string) {
		q(dst, `INSERT INTO videos (library_id, content_hash, path, filename, size_bytes, mtime, title, duration_sec)
			VALUES ($1::uuid, encode(sha256(convert_to($2, 'UTF8')), 'hex'), '/m/'||$2||'/v.mp4', 'v.mp4', 1, now(), $2, 3600) RETURNING id::text`, lib, name)
	}
	video(&h.permitted, h.lib, "permitted")
	video(&h.cc, h.lib, "cc")
	video(&h.other, h.lib, "other")
	video(&h.quran, h.reciterLib, "fatiha")
	x(`INSERT INTO youtube_channels (channel_id, url, title) VALUES ('UCxxxx', 'https://youtube.com/channel/UCxxxx', 'Lessons Channel'),
		('UCcc', 'https://youtube.com/channel/UCcc', 'Open Talks'), ('UCother', 'https://youtube.com/channel/UCother', 'Other')`)
	x(`INSERT INTO youtube_videos (youtube_id, channel_id, title, license) VALUES ('yt-perm', 'UCxxxx', 'Lesson 1', 'standard'),
		('yt-cc', 'UCcc', 'Open talk', 'cc_by'), ('yt-other', 'UCother', 'Other talk', 'standard')`)
	for vid, yid := range map[string]string{h.permitted: "yt-perm", h.cc: "yt-cc", h.other: "yt-other"} {
		x(`INSERT INTO media_metadata_enrichment (video_id, provider, external_id, confidence) VALUES ($1::uuid, 'youtube', 'youtube:'||$2, 1)`, vid, yid)
	}
	// tonight's schedule: an hour of each, starting in an hour
	start := time.Now().Add(time.Hour)
	for i, vid := range []string{h.permitted, h.cc, h.other, h.quran} {
		s := start.Add(time.Duration(i) * time.Hour)
		x(`INSERT INTO channel_programs (channel_id, seq, start_at, end_at, source_duration, video_id)
			VALUES ($1::uuid, $2, $3, $4, 3600000, $5::uuid)`, h.ch, i+1, s, s.Add(time.Hour), vid)
	}
	return h
}

func (h pubHome) reason(t *testing.T, vid string) string {
	t.Helper()
	r, err := (&Store{DB: h.db}).ItemReason(context.Background(), vid)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// T07 at the API: a recorded permission, a withdrawal and a bulk own mark are stored on
// the home and change what is publishable at once; nothing reaches the cloud. The
// acceptance's CC BY item is publishable with its attribution.
func TestPermissionsAndMarks(t *testing.T) {
	h := newPubHome(t)
	ctx := context.Background()
	cloud := &fakeCloud{}
	svc := &Service{Store: &Store{DB: h.db}, Cloud: cloud}
	amina := Admin{Name: "Amina"}

	if got := h.reason(t, h.cc); got != ReasonCCBy {
		t.Fatalf("CC BY item: %q", got)
	}
	var attr string
	if err := h.db.QueryRow(`SELECT video_attribution($1::uuid)::text`, h.cc).Scan(&attr); err != nil ||
		attr != `{"url": "https://www.youtube.com/watch?v=yt-cc", "kind": "cc_by", "title": "Open talk", "author": "Open Talks", "licence": "CC BY"}` {
		t.Fatalf("attribution %s %v", attr, err)
	}
	if got := h.reason(t, h.permitted); got != "" {
		t.Fatalf("standard licence, no permission: %q", got)
	}

	// a proof link is required, and must be a link
	var p *Problem
	if _, err := svc.RecordSource(ctx, PermissionRequest{Kind: KindChannel, Ref: "UCxxxx", ProofURL: "about page"}, amina); !errors.As(err, &p) || p.Refusal.Code != "badProof" {
		t.Fatalf("no link: %v", err)
	}
	src, err := svc.RecordSource(ctx, PermissionRequest{Kind: KindChannel, Ref: "UCxxxx", Label: "Lessons Channel",
		ProofURL: "https://www.youtube.com/channel/UCxxxx/about", Credit: "Courtesy of Lessons Channel"}, amina)
	if err != nil {
		t.Fatal(err)
	}
	if src.RecordedBy != "Amina" || src.RevokedAt != nil {
		t.Fatalf("recorded: %+v", src)
	}
	if _, err := svc.RecordSource(ctx, PermissionRequest{Kind: KindChannel, Ref: "UCxxxx", ProofURL: "https://x.test"}, amina); !errors.As(err, &p) || p.Refusal.Code != "sourceRecorded" {
		t.Fatalf("twice: %v", err)
	}
	if got := h.reason(t, h.permitted); got != ReasonPermission {
		t.Fatalf("after the permission: %q", got)
	}
	if err := h.db.QueryRow(`SELECT video_attribution($1::uuid)->>'credit'`, h.permitted).Scan(&attr); err != nil || attr != "Courtesy of Lessons Channel" {
		t.Fatalf("credit %q %v", attr, err)
	}
	if got := h.reason(t, h.other); got != "" {
		t.Fatalf("another channel's video: %q", got)
	}

	// withdrawn: publishable no more, at once; the record stays
	if _, err := svc.WithdrawSource(ctx, src.ID, "the channel asked"); err != nil {
		t.Fatal(err)
	}
	if got := h.reason(t, h.permitted); got != "" {
		t.Fatalf("after the withdrawal: %q", got)
	}
	if _, err := svc.WithdrawSource(ctx, src.ID, ""); !errors.Is(err, ErrNoSource) {
		t.Fatalf("withdrawn twice: %v", err)
	}
	list, err := svc.Store.Sources(ctx)
	if err != nil || len(list) != 1 || list[0].RevokedAt == nil || list[0].RevokedReason != "the channel asked" {
		t.Fatalf("the record: %+v %v", list, err)
	}

	// bulk own: a folder of the library in one action
	n, err := svc.MarkItems(ctx, MarkRequest{Reason: ReasonOwn, LibraryID: h.lib, Folder: "/m/other"}, amina)
	if err != nil || n != 1 {
		t.Fatalf("folder own: %d %v", n, err)
	}
	if got := h.reason(t, h.other); got != ReasonOwn {
		t.Fatalf("own: %q", got)
	}
	// cleared is one item with its proof
	if _, err := svc.MarkItems(ctx, MarkRequest{Reason: ReasonCleared, VideoIDs: []string{h.permitted}}, amina); !errors.As(err, &p) || p.Refusal.Code != "badProof" {
		t.Fatalf("cleared without proof: %v", err)
	}
	if _, err := svc.MarkItems(ctx, MarkRequest{Reason: ReasonCleared, VideoIDs: []string{h.permitted}, ProofURL: "https://x.test/ok"}, amina); err != nil {
		t.Fatal(err)
	}
	if got := h.reason(t, h.permitted); got != ReasonCleared {
		t.Fatalf("cleared: %q", got)
	}
	if _, err := svc.MarkItems(ctx, MarkRequest{Reason: "none", VideoIDs: []string{h.permitted, h.other}}, amina); err != nil {
		t.Fatal(err)
	}
	if got := h.reason(t, h.other); got != "" {
		t.Fatalf("cleared mark: %q", got)
	}
	if cloud.calls() != 0 {
		t.Fatalf("permissions reached the cloud: %d calls", cloud.calls())
	}
}

// T05: a Quran recitation is publishable only when its reciter (its library) has a
// permission, or it is CC or own.
func TestReciterIsASource(t *testing.T) {
	h := newPubHome(t)
	ctx := context.Background()
	svc := &Service{Store: &Store{DB: h.db}}
	if got := h.reason(t, h.quran); got != "" {
		t.Fatalf("no permission: %q", got)
	}
	src, err := svc.RecordSource(ctx, PermissionRequest{Kind: KindSpeaker, Ref: "reciter a", ProofURL: "https://reciter.test/permission"}, Admin{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	if got := h.reason(t, h.quran); got != ReasonPermission {
		t.Fatalf("reciter's permission: %q", got)
	}
	if _, err := svc.WithdrawSource(ctx, src.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := h.reason(t, h.quran); got != "" {
		t.Fatalf("withdrawn: %q", got)
	}
}

// §4 and check 3: the share counts only publishable hours, the suggestions name the
// sources that would add the most, and check 3's seconds follow the marks.
func TestShareAndSuggestions(t *testing.T) {
	h := newPubHome(t)
	ctx := context.Background()
	svc := &Service{Store: &Store{DB: h.db}}
	sh, err := svc.Store.ChannelShare(ctx, h.ch, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if sh.ScheduledSeconds != 4*3600 || sh.PublishableSeconds != 3600 || sh.Percent != 25 {
		t.Fatalf("share: %+v", sh)
	}
	if len(sh.Suggestions) != 3 {
		t.Fatalf("suggestions: %+v", sh.Suggestions)
	}
	var yt, lib int
	for _, s := range sh.Suggestions {
		switch {
		case s.Kind == KindChannel && s.Ref == "UCxxxx":
			yt++
			if s.URL != "https://www.youtube.com/channel/UCxxxx/about" || s.Label != "Lessons Channel" || s.Seconds != 3600 {
				t.Fatalf("channel suggestion: %+v", s)
			}
		case s.Kind == KindSpeaker && s.Ref == "Reciter A":
			lib++
		}
	}
	if yt != 1 || lib != 1 {
		t.Fatalf("suggestions: %+v", sh.Suggestions)
	}
	now := time.Now()
	secs, err := MarkedPublishable{}.Seconds(ctx, h.db, h.ch, now, now.Add(24*time.Hour))
	if err != nil || secs != 3600 {
		t.Fatalf("check 3's seconds: %d %v", secs, err)
	}
	v, err := svc.PublishableView(ctx)
	if err != nil || !v.PublishableOnly || len(v.Shares) != 1 {
		t.Fatalf("view: %+v %v", v, err)
	}
}

// §3: publishable_only is on by default, turned off only with the typed confirmation,
// and channel_publishable_only follows it while a channel is published.
func TestPublishableOnlySwitch(t *testing.T) {
	h := newPubHome(t)
	ctx := context.Background()
	svc := &Service{Store: &Store{DB: h.db}}
	limited := func() bool {
		var b bool
		if err := h.db.QueryRow(`SELECT channel_publishable_only($1::uuid)`, h.ch).Scan(&b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	if limited() {
		t.Fatal("a draft channel is limited")
	}
	if _, err := h.db.Exec(`INSERT INTO broadcast_channels (channel_id, slug, state) VALUES ($1::uuid, 'lessons', 'live')`, h.ch); err != nil {
		t.Fatal(err)
	}
	if !svc.Store.PublishableOnly(ctx) || !limited() {
		t.Fatal("published: not limited by default")
	}
	var p *Problem
	if _, err := svc.SetPublishableOnly(ctx, false, "yes"); !errors.As(err, &p) || p.Refusal.Code != "confirmOff" {
		t.Fatalf("off without the phrase: %v", err)
	}
	if on, err := svc.SetPublishableOnly(ctx, false, "Turn off"); err != nil || on {
		t.Fatalf("off: %v %v", on, err)
	}
	if svc.Store.PublishableOnly(ctx) || limited() {
		t.Fatal("still limited after turning it off")
	}
	// the published channel's schedule is rewritten at the next top-up, not only past it
	var stale bool
	if err := h.db.QueryRow(`SELECT bool_or(stale) FROM channel_schedule_state WHERE channel_id::text = $1`, h.ch).Scan(&stale); err != nil || !stale {
		t.Fatalf("schedule not marked stale: %v %v", stale, err)
	}
	if on, err := svc.SetPublishableOnly(ctx, true, ""); err != nil || !on || !limited() {
		t.Fatalf("on again: %v %v", on, err)
	}
}
