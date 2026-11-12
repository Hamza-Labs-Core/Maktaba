package router_test

// Epic 57 review, finding 3: the admin's tier on a video and the job that works out
// the video's tier are stored together.

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// The recompute that follows the admin's change fails (here: the write of
// videos.age_tier is refused). Before the fix the admin's 18 was stored with no job,
// and no trigger watches age_tier_admin, so the video kept its looser tier for good.
func TestP57VideoTierIsQueuedWithTheChange(t *testing.T) {
	t.Parallel()
	e := setupTiers(t)
	lib := uuid.NewString()
	e.exec(`INSERT INTO libraries (id, name, roots, age_tier) VALUES ($1, 'atomic', '{/media/atomic}', 9)`, lib)
	v := e.video(lib)
	e.want(v, 9, "library_default")

	e.exec(`CREATE FUNCTION test_refuse_tier() RETURNS trigger LANGUAGE plpgsql AS $f$
		BEGIN RAISE EXCEPTION 'the tier cannot be written now'; END $f$`)
	e.exec(`CREATE TRIGGER test_refuse_tier BEFORE UPDATE OF age_tier ON videos
		FOR EACH ROW EXECUTE FUNCTION test_refuse_tier()`)
	code, out, _ := e.do("admin", http.MethodPut, "/api/videos/"+v+"/age-tier", `{"tier": 18}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("PUT while the tier cannot be written: %d %v, want 500", code, out)
	}
	var admin, jobs int
	if err := e.db.QueryRow(`SELECT COALESCE(age_tier_admin, -1) FROM videos WHERE id = $1`, v).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	if err := e.db.QueryRow(`SELECT count(*) FROM parental_recompute_jobs WHERE kind = 'video' AND subject = $1`, v).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	// both or neither: the admin's tier is stored, so its job must be too
	if admin != 18 || jobs == 0 {
		t.Fatalf("after the failed answer: age_tier_admin = %d with %d recompute jobs; want 18 with a job", admin, jobs)
	}

	// the fault passes: the loop rates the video with no further request
	e.exec(`DROP TRIGGER test_refuse_tier ON videos`)
	e.want(v, 18, "admin_video")

	// a video that is not there stores nothing and queues nothing
	missing := uuid.NewString()
	if code, _, _ := e.do("admin", http.MethodPut, "/api/videos/"+missing+"/age-tier", `{"tier": 18}`); code != http.StatusNotFound {
		t.Fatalf("PUT on a missing video: %d, want 404", code)
	}
	if err := e.db.QueryRow(`SELECT count(*) FROM parental_recompute_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("%d recompute jobs after a 404 (%v), want 0", jobs, err)
	}

	// the usual case still answers what the video resolves to, and leaves no work behind
	e.put("/api/videos/"+v+"/age-tier", `{"tier": null}`, 9, "library_default", 1)
	e.want(v, 9, "library_default")
	if err := e.db.QueryRow(`SELECT count(*) FROM parental_recompute_jobs`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("%d recompute jobs left after the queue was worked (%v)", jobs, err)
	}
}
