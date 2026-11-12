package parental

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Kind is what a recompute job names: one video, or what contains or matches the
// videos to work through.
type Kind string

const (
	// KindAll is every library; it becomes one KindLibrary job for each.
	KindAll Kind = "all"
	// KindLibrary is every video of a library (subject: the library's id).
	KindLibrary Kind = "library"
	// KindSeries is a series's episodes (subject: the series's id).
	KindSeries Kind = "series"
	// KindCollection is a collection's videos (subject: the collection's id).
	KindCollection Kind = "collection"
	// KindYouTubeSource is the videos a YouTube source downloaded (subject: its id).
	KindYouTubeSource Kind = "youtube_source"
	// KindYouTubeVideo is the videos matched to one YouTube video (subject: its
	// YouTube id).
	KindYouTubeVideo Kind = "youtube_video"
	// KindVideo is one video (subject: the video's id).
	KindVideo Kind = "video"
)

// BatchSize is how many videos one batch of a recompute job works.
const BatchSize = 1000

// pollEvery is how often the loop looks at the queue when nothing woke it: the
// stages' changes arrive through database triggers, which wake nothing here.
const pollEvery = 5 * time.Second

// Execer is a *sql.DB or a *sql.Tx: Enqueue runs in the caller's transaction when
// the job must appear with the change that asks for it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Recomputer keeps videos.age_tier and age_tier_source. It is their only writer:
// everything else changes what a tier is resolved from and puts a job in
// parental_recompute_jobs (the admin routes through Enqueue, the stages through the
// triggers of slot 0220). A job worked twice gives the same tiers.
type Recomputer struct {
	db     *sql.DB
	table  *Table
	logger *slog.Logger
	batch  int
	wake   chan struct{}
}

// NewRecomputer returns the recompute over db, resolving with the embedded ratings.
func NewRecomputer(db *sql.DB, logger *slog.Logger) *Recomputer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Recomputer{db: db, table: Default(), logger: logger, batch: BatchSize, wake: make(chan struct{}, 1)}
}

// Enqueue puts a recompute job in the queue. The subject of a kind that names a
// row by its id must be a UUID.
func (r *Recomputer) Enqueue(ctx context.Context, q Execer, kind Kind, subject string) error {
	if !validSubject(kind, subject) {
		return fmt.Errorf("parental: %q is no subject for a %s recompute", subject, kind)
	}
	if _, err := q.ExecContext(ctx,
		`INSERT INTO parental_recompute_jobs (kind, subject) VALUES ($1, $2)`, string(kind), subject); err != nil {
		return fmt.Errorf("parental: enqueue %s recompute: %w", kind, err)
	}
	return nil
}

// Wake has the loop look at the queue now instead of at its next poll. Call it
// after the transaction that enqueued has committed.
func (r *Recomputer) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Run works the queue until ctx ends. One batch failing is logged and tried again
// at the next poll, behind the jobs that have not failed (see Step).
func (r *Recomputer) Run(ctx context.Context) error {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	for {
		if _, err := r.Drain(ctx); err != nil && ctx.Err() == nil {
			r.logger.Error("parental recompute failed", "event", "parental.recompute_failed", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		case <-r.wake:
		}
	}
}

// Drain works the queue until it is empty and returns how many steps it took. A
// job whose batch fails does not stop the others: they are worked first, and Drain
// returns the first error once the only work left is a job that already failed on
// its own in this call (it is tried again at the next call).
func (r *Recomputer) Drain(ctx context.Context) (int, error) {
	var (
		steps    int
		firstErr error
		// the jobs that failed when worked alone (a batch of video jobs that fails is
		// worked again one job at a time, so that only the one at fault fails)
		failedAlone = map[int64]bool{}
	)
	for {
		if err := ctx.Err(); err != nil {
			return steps, err
		}
		worked, jobs, err := r.step(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if len(jobs) == 0 { // nothing was claimed: the queue itself cannot be read
				return steps, firstErr
			}
			if len(jobs) == 1 {
				if failedAlone[jobs[0]] {
					return steps, firstErr
				}
				failedAlone[jobs[0]] = true
			}
			continue
		}
		if !worked {
			return steps, firstErr
		}
		steps++
	}
}

// Step works one batch of one job nobody else holds: up to BatchSize videos of a
// container's job, up to BatchSize single-video jobs, or the expansion of an "all"
// job. It reports false when the queue had nothing to work.
//
// The queue is worked by (attempts, batches, id). A job whose batch failed has its
// attempts raised and so goes behind every job that has failed less: one job that
// fails every time holds nothing up, and it is never dropped (a tier nothing gives
// any more must not stay because its recompute was given up). A job that has done
// fewer batches goes first, so single-video jobs and a newly set series or
// collection are worked between the batches of a long backfill, not after it.
func (r *Recomputer) Step(ctx context.Context) (bool, error) {
	worked, _, err := r.step(ctx)
	return worked, err
}

// step is Step; it also returns the jobs it had claimed when it failed, after
// raising their attempts.
func (r *Recomputer) step(ctx context.Context) (bool, []int64, error) {
	worked, claimed, err := r.stepTx(ctx)
	if err == nil || len(claimed) == 0 || ctx.Err() != nil {
		// a cancelled context is the API stopping, not the job's failure
		return worked, nil, err
	}
	if _, bumpErr := r.db.ExecContext(ctx,
		`UPDATE parental_recompute_jobs SET attempts = attempts + 1 WHERE id = ANY($1::bigint[])`,
		pq.Array(claimed)); bumpErr != nil {
		r.logger.Error("parental recompute: a failed job's attempts cannot be raised",
			"event", "parental.recompute_attempts_failed", "error", bumpErr.Error())
	}
	return false, claimed, err
}

// stepTx works one batch in one transaction and returns the jobs it claimed.
func (r *Recomputer) stepTx(ctx context.Context) (bool, []int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, fmt.Errorf("parental: begin recompute: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		id       int64
		kind     string
		subject  string
		attempts int
		cursorAt sql.NullTime
		cursorID sql.NullString
	)
	err = tx.QueryRowContext(ctx, `
		SELECT id, kind, subject, attempts, cursor_at, cursor_id
		  FROM parental_recompute_jobs
		 ORDER BY attempts, batches, id
		 LIMIT 1
		   FOR UPDATE SKIP LOCKED`).Scan(&id, &kind, &subject, &attempts, &cursorAt, &cursorID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, nil
	}
	if err != nil {
		return false, nil, fmt.Errorf("parental: claim recompute job: %w", err)
	}
	claimed := []int64{id}

	switch k := Kind(kind); {
	case k == KindAll:
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO parental_recompute_jobs (kind, subject)
			SELECT 'library', id::text FROM libraries ORDER BY created_at, id`); err != nil {
			return false, claimed, fmt.Errorf("parental: expand the recompute of every library: %w", err)
		}
		if err := deleteJobs(ctx, tx, []int64{id}); err != nil {
			return false, claimed, err
		}
	case !validSubject(k, subject):
		// A job nothing can work would stay in the queue for ever.
		r.logger.Warn("parental recompute job dropped", "event", "parental.recompute_job_invalid",
			"kind", kind, "subject", subject)
		if err := deleteJobs(ctx, tx, []int64{id}); err != nil {
			return false, claimed, err
		}
	case k == KindVideo:
		if claimed, err = r.stepVideos(ctx, tx, attempts); err != nil {
			return false, claimed, err
		}
	default:
		if err := r.stepScope(ctx, tx, id, k, subject, cursorAt, cursorID); err != nil {
			return false, claimed, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, claimed, fmt.Errorf("parental: commit recompute: %w", err)
	}
	return true, nil, nil
}

// stepVideos works up to a batch of the waiting single-video jobs at once, and
// returns the jobs it claimed. Jobs that failed before (attempts over 0) are worked
// one at a time: they failed as a batch, and alone only the video that cannot be
// rated fails again.
func (r *Recomputer) stepVideos(ctx context.Context, tx *sql.Tx, attempts int) ([]int64, error) {
	limit := r.batch
	if attempts > 0 {
		limit = 1
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, subject
		  FROM parental_recompute_jobs
		 WHERE kind = 'video' AND attempts = $2
		 ORDER BY id
		 LIMIT $1
		   FOR UPDATE SKIP LOCKED`, limit, attempts)
	if err != nil {
		return nil, fmt.Errorf("parental: claim video recompute jobs: %w", err)
	}
	var (
		jobs []int64
		ids  []string
		seen = map[string]bool{}
	)
	for rows.Next() {
		var (
			id      int64
			subject string
		)
		if err := rows.Scan(&id, &subject); err != nil {
			_ = rows.Close()
			return jobs, fmt.Errorf("parental: read video recompute jobs: %w", err)
		}
		jobs = append(jobs, id)
		if validSubject(KindVideo, subject) && !seen[subject] {
			seen[subject] = true
			ids = append(ids, subject)
		}
	}
	if err := closeRows(rows); err != nil {
		return jobs, fmt.Errorf("parental: read video recompute jobs: %w", err)
	}
	if _, err := r.recompute(ctx, tx, ids); err != nil {
		return jobs, err
	}
	return jobs, deleteJobs(ctx, tx, jobs)
}

// scopeWhere is, for each kind that names many videos, the condition that picks
// them ($1 is the job's subject).
var scopeWhere = map[Kind]string{
	KindLibrary:       `v.library_id = $1::uuid`,
	KindSeries:        `EXISTS (SELECT 1 FROM series_episodes e WHERE e.series_id = $1::uuid AND e.video_id = v.id)`,
	KindCollection:    `EXISTS (SELECT 1 FROM collection_items ci WHERE ci.collection_id = $1::uuid AND ci.video_id = v.id)`,
	KindYouTubeSource: `EXISTS (SELECT 1 FROM youtube_downloads d WHERE d.source_id = $1::uuid AND d.video_id = v.id)`,
	KindYouTubeVideo: `EXISTS (SELECT 1 FROM media_metadata_enrichment m
		                    WHERE m.video_id = v.id AND m.provider = 'youtube'
		                      AND m.external_id IN ($1::text, 'youtube:' || $1::text))`,
}

// stepScope works the next batch of a job that names many videos, newest first,
// and keeps the job's place; the job is deleted with its last batch.
func (r *Recomputer) stepScope(ctx context.Context, tx *sql.Tx, job int64, kind Kind, subject string,
	cursorAt sql.NullTime, cursorID sql.NullString) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT v.id, v.created_at
		  FROM videos v
		 WHERE `+scopeWhere[kind]+`
		   AND ($2::timestamptz IS NULL OR (v.created_at, v.id) < ($2::timestamptz, $3::uuid))
		 ORDER BY v.created_at DESC, v.id DESC
		 LIMIT $4`, subject, cursorAt, cursorID, r.batch+1)
	if err != nil {
		return fmt.Errorf("parental: list the videos of a %s recompute: %w", kind, err)
	}
	var (
		ids []string
		ats []time.Time
	)
	for rows.Next() {
		var (
			id string
			at time.Time
		)
		if err := rows.Scan(&id, &at); err != nil {
			_ = rows.Close()
			return fmt.Errorf("parental: read the videos of a %s recompute: %w", kind, err)
		}
		ids = append(ids, id)
		ats = append(ats, at)
	}
	if err := closeRows(rows); err != nil {
		return fmt.Errorf("parental: read the videos of a %s recompute: %w", kind, err)
	}
	// One more than a batch was asked for: its presence says another batch is due.
	more := len(ids) > r.batch
	if more {
		ids, ats = ids[:r.batch], ats[:r.batch]
	}
	changed, err := r.recompute(ctx, tx, ids)
	if err != nil {
		return err
	}
	r.logger.Info("parental recompute batch", "event", "parental.recompute_batch",
		"kind", string(kind), "subject", subject, "videos", len(ids), "changed", changed, "more", more)
	if !more {
		return deleteJobs(ctx, tx, []int64{job})
	}
	last := len(ids) - 1
	if _, err := tx.ExecContext(ctx, `
		UPDATE parental_recompute_jobs
		   SET cursor_at = $2, cursor_id = $3::uuid, batches = batches + 1
		 WHERE id = $1`, job, ats[last], ids[last]); err != nil {
		return fmt.Errorf("parental: keep the place of a %s recompute: %w", kind, err)
	}
	return nil
}

// RecomputeVideo resolves one video now and returns its tier; the admin's change
// to a single video answers with it.
func (r *Recomputer) RecomputeVideo(ctx context.Context, videoID string) (Result, error) {
	if !validSubject(KindVideo, videoID) {
		return Result{}, fmt.Errorf("parental: %q is no video id", videoID)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, fmt.Errorf("parental: begin recompute: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := r.recompute(ctx, tx, []string{videoID}); err != nil {
		return Result{}, err
	}
	var (
		tier   sql.NullInt64
		source sql.NullString
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT age_tier, age_tier_source FROM videos WHERE id = $1::uuid`, videoID).Scan(&tier, &source); err != nil {
		return Result{}, fmt.Errorf("parental: read the video's tier: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Result{}, fmt.Errorf("parental: commit recompute: %w", err)
	}
	res := Result{Source: Source(source.String)}
	if tier.Valid {
		n := int(tier.Int64)
		res.Tier = &n
	}
	return res, nil
}

// CountVideos is how many videos a job of this kind and subject would work: the
// admin routes answer with it.
func (r *Recomputer) CountVideos(ctx context.Context, kind Kind, subject string) (int, error) {
	where, ok := scopeWhere[kind]
	if !ok || !validSubject(kind, subject) {
		return 0, fmt.Errorf("parental: no videos to count for a %s recompute of %q", kind, subject)
	}
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT count(*) FROM videos v WHERE `+where, subject).Scan(&n); err != nil {
		return 0, fmt.Errorf("parental: count the videos of a %s recompute: %w", kind, err)
	}
	return n, nil
}

// loadVideos reads, for each video, what its tier is resolved from. The YouTube
// match is the best one, picked as slot 0164 picks it; the guide's ratings are read
// through the partial index of slot 0220, whose condition is repeated here. The
// detected tier ($2 is the confidence line) is left out under the line and once an
// admin has reviewed it.
//
// Each video's row is locked (FOR NO KEY UPDATE, in id order so two batches never wait
// on each other in a ring) from the read to the commit. Two recomputes of one video,
// the loop's batch and an admin's own change, or two API processes, then run one
// after the other: the later one reads what the earlier one's transaction saw
// committed, so an older reading can never be written over a newer tier.
const loadVideos = `
SELECT v.id, v.age_tier, COALESCE(v.age_tier_source, ''), v.age_tier_admin,
       COALESCE(v.metadata->>'content_rating', ''), l.age_tier,
       ARRAY(SELECT s.age_tier::int FROM series_episodes e JOIN series s ON s.id = e.series_id
              WHERE e.video_id = v.id AND s.age_tier IS NOT NULL
             UNION ALL
             SELECT c.age_tier::int FROM collection_items ci JOIN collections c ON c.id = ci.collection_id
              WHERE ci.video_id = v.id AND c.age_tier IS NOT NULL
             UNION ALL
             SELECT ys.age_tier::int FROM youtube_downloads d JOIN youtube_sources ys ON ys.id = d.source_id
              WHERE d.video_id = v.id AND ys.age_tier IS NOT NULL),
       ARRAY(SELECT DISTINCT p.detail->>'contentRating' FROM plex_items p
              WHERE p.video_id = v.id AND COALESCE(p.detail->>'contentRating', '') <> ''),
       ARRAY(SELECT DISTINCT cp.title_snapshot->>'rating' FROM channel_programs cp
              WHERE cp.video_id = v.id AND cp.video_id IS NOT NULL AND cp.title_snapshot ? 'rating'
                AND COALESCE(cp.title_snapshot->>'rating', '') <> ''),
       (SELECT y.age_limit FROM youtube_videos y
         WHERE y.youtube_id = (SELECT CASE WHEN m.external_id LIKE 'youtube:%'
                                           THEN substr(m.external_id, 9) ELSE m.external_id END
                                 FROM media_metadata_enrichment m
                                WHERE m.video_id = v.id AND m.provider = 'youtube'
                                ORDER BY m.confidence DESC, m.created_at DESC, m.id
                                LIMIT 1)),
       (SELECT cs.tier FROM video_content_signals cs
         WHERE cs.video_id = v.id AND cs.tier IS NOT NULL AND cs.reviewed_at IS NULL
           AND cs.confidence >= $2::real)
  FROM videos v
  LEFT JOIN libraries l ON l.id = v.library_id
 WHERE v.id = ANY($1::uuid[])
 ORDER BY v.id
   FOR NO KEY UPDATE OF v`

// recompute resolves the given videos and writes the tiers that changed; it
// returns how many did.
func (r *Recomputer) recompute(ctx context.Context, tx *sql.Tx, ids []string) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	// an unreadable line fails the recompute (the job stays and is worked again):
	// a detected tier is never counted, or dropped, on a guess
	line, err := DetectLine(ctx, tx)
	if err != nil {
		return 0, err
	}
	rows, err := tx.QueryContext(ctx, loadVideos, pq.Array(ids), line)
	if err != nil {
		return 0, fmt.Errorf("parental: load videos to rate: %w", err)
	}
	var (
		changedIDs     []string
		changedTiers   []int64
		changedSources []string
	)
	for rows.Next() {
		var (
			id         string
			curTier    sql.NullInt64
			curSource  string
			admin      sql.NullInt64
			plugin     string
			library    sql.NullInt64
			containers pq.Int64Array
			plex       pq.StringArray
			guide      pq.StringArray
			ageLimit   sql.NullInt64
			detected   sql.NullInt64
		)
		if err := rows.Scan(&id, &curTier, &curSource, &admin, &plugin, &library,
			&containers, &plex, &guide, &ageLimit, &detected); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("parental: read a video to rate: %w", err)
		}
		v := Video{
			AdminTier:       nullInt(admin),
			PlexRatings:     plex,
			GuideRatings:    guide,
			YouTubeAgeLimit: nullInt(ageLimit),
			PluginRating:    plugin,
			DetectedTier:    nullInt(detected),
			LibraryDefault:  nullInt(library),
		}
		for _, c := range containers {
			v.ContainerTiers = append(v.ContainerTiers, int(c))
		}
		res := r.table.Resolve(v)
		// -1 stands for "unrated" in the array the update reads: NULL there.
		tier := int64(-1)
		if res.Tier != nil {
			tier = int64(*res.Tier)
		}
		cur := int64(-1)
		if curTier.Valid {
			cur = curTier.Int64
		}
		if tier == cur && string(res.Source) == curSource {
			continue
		}
		changedIDs = append(changedIDs, id)
		changedTiers = append(changedTiers, tier)
		changedSources = append(changedSources, string(res.Source))
	}
	if err := closeRows(rows); err != nil {
		return 0, fmt.Errorf("parental: read videos to rate: %w", err)
	}
	if len(changedIDs) == 0 {
		return 0, nil
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE videos v
		   SET age_tier = NULLIF(x.tier, -1)::smallint, age_tier_source = NULLIF(x.source, '')
		  FROM unnest($1::uuid[], $2::int[], $3::text[]) AS x(id, tier, source)
		 WHERE v.id = x.id`,
		pq.Array(changedIDs), pq.Array(changedTiers), pq.Array(changedSources)); err != nil {
		return 0, fmt.Errorf("parental: write tiers: %w", err)
	}
	return len(changedIDs), nil
}

func deleteJobs(ctx context.Context, tx *sql.Tx, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM parental_recompute_jobs WHERE id = ANY($1::bigint[])`, pq.Array(ids)); err != nil {
		return fmt.Errorf("parental: finish recompute jobs: %w", err)
	}
	return nil
}

func closeRows(rows *sql.Rows) error {
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	return rows.Close()
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// validSubject says whether subject can name what a job of this kind works.
func validSubject(kind Kind, subject string) bool {
	switch kind {
	case KindAll:
		return true
	case KindYouTubeVideo:
		return subject != ""
	case KindLibrary, KindSeries, KindCollection, KindYouTubeSource, KindVideo:
		_, err := uuid.Parse(subject)
		return err == nil
	}
	return false
}
