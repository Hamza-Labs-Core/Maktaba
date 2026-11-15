package rehearsal

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// fakeSeaweed answers like SeaweedFS 3.97's S3 gateway. A bucket made while poisoned is cached
// as missing: writes into it fail until a restart (or a bucket delete) clears the cache. A
// bucket deleted and made again before the next heartbeat wedges: every write fails, restart
// or not.
type fakeSeaweed struct {
	bucket, object bool
	poisonNext     int // buckets made from now on that come out cached as missing
	missing        bool
	wedged         bool
	deletedBeat    bool // a bucket delete whose volumes the next heartbeat still has to drop
	bucketDeletes  int
	restarts       int
	down           int // calls answered with nothing before the gateway serves
	calls          []string
}

const notFound = `<Error><Code>InternalError</Code><Message>bucket directory not found</Message></Error>`

func (f *fakeSeaweed) do(method, path string, _ ...string) (int, string) {
	f.calls = append(f.calls, method+" "+path)
	if f.down > 0 {
		f.down--
		return 0, "curl: (7) Failed to connect"
	}
	switch {
	case method == "PUT" && path == "/b":
		if f.bucket {
			return 409, s3Why(`<Error><Code>BucketAlreadyExists</Code></Error>`)
		}
		if f.deletedBeat {
			f.wedged = true
		}
		f.bucket = true
		if f.poisonNext > 0 {
			f.poisonNext--
			f.missing = true
		}
		return 200, ""
	case method == "DELETE" && path == "/b":
		f.bucket, f.missing = false, false
		f.bucketDeletes++
		f.deletedBeat = true
		return 204, ""
	case method == "PUT" && path == "/b/probe":
		if !f.bucket {
			return 404, s3Why(`<Error><Code>NoSuchBucket</Code></Error>`)
		}
		if f.missing || f.wedged {
			return 500, s3Why(notFound)
		}
		f.object = true
		return 200, ""
	case method == "GET" && path == "/b/probe":
		if !f.object {
			return 404, s3Why(`<Error><Code>NoSuchKey</Code></Error>`)
		}
		return 200, ""
	case method == "DELETE" && path == "/b/probe":
		f.object = false
		return 204, ""
	}
	return 400, ""
}

func (f *fakeSeaweed) restart() {
	f.restarts++
	f.missing = false // the gateway's cache starts empty; the bucket and its volumes stay
}

// clock is a fake clock that sleep moves.
type clock struct{ t time.Time }

func (c *clock) steps(restarts int) bucketSteps {
	return bucketSteps{stuck: 15 * time.Second, restarts: restarts, now: func() time.Time { return c.t },
		sleep: func(d time.Duration) { c.t = c.t.Add(d) }}
}

func make1(t *testing.T, f *fakeSeaweed, restarts int) ([]string, error) {
	t.Helper()
	var logs []string
	c := &clock{t: time.Unix(0, 0)}
	err := makeBucket(f.do, "b", f.restart, func(format string, a ...any) { logs = append(logs, fmt.Sprintf(format, a...)) }, c.steps(restarts))
	return logs, err
}

func TestMakeBucketFirstTry(t *testing.T) {
	f := &fakeSeaweed{}
	if logs, err := make1(t, f, 2); err != nil || len(logs) != 0 || f.restarts != 0 {
		t.Fatalf("err %v, logs %q, restarts %d; want a quiet first try", err, logs, f.restarts)
	}
	if f.object {
		t.Error("the probe object was left in the bucket")
	}
}

// The failure on PR 1374 and PR 1380: the bucket comes out cached as missing.
func TestMakeBucketRestartsAGatewayThatCachedTheBucketAsMissing(t *testing.T) {
	f := &fakeSeaweed{poisonNext: 1}
	logs, err := make1(t, f, 2)
	if err != nil {
		t.Fatal(err)
	}
	if f.restarts != 1 || f.bucketDeletes != 0 {
		t.Errorf("restarts %d, bucket deletes %d; want one restart and no delete", f.restarts, f.bucketDeletes)
	}
	joined := strings.Join(logs, "\n")
	for _, want := range []string{"PUT /b/probe: 500 InternalError: bucket directory not found", "restarting its gateway (1 of 2)", "bucket b serves"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the log lacks %q:\n%s", want, joined)
		}
	}
	if n := strings.Count(joined, "not ready"); n != 1 {
		t.Errorf("%d not-ready lines; a reason is logged once until it changes:\n%s", n, joined)
	}
}

// The old recovery against the same gateway: delete and remake the bucket.
func TestDeletingAndRemakingTheBucketWedgesIt(t *testing.T) {
	f := &fakeSeaweed{poisonNext: 1}
	for range 50 {
		ok, _ := bucketReady(f.do, "b")
		if ok {
			t.Fatal("delete and remake recovered, the fake no longer models the wedge")
		}
		_, _ = f.do("DELETE", "/b")
	}
}

func TestMakeBucketWaitsForTheGateway(t *testing.T) {
	f := &fakeSeaweed{down: 3}
	if _, err := make1(t, f, 2); err != nil || f.restarts != 0 {
		t.Fatalf("err %v, restarts %d", err, f.restarts)
	}
}

func TestMakeBucketTakesABucketThatIsThere(t *testing.T) {
	f := &fakeSeaweed{bucket: true}
	if _, err := make1(t, f, 2); err != nil {
		t.Fatal(err)
	}
}

func TestMakeBucketGivesUpAfterItsRestarts(t *testing.T) {
	f := &fakeSeaweed{wedged: true}
	_, err := make1(t, f, 2)
	if err == nil || f.restarts != 2 {
		t.Fatalf("err %v, restarts %d; want a failure after two restarts", err, f.restarts)
	}
	if !strings.Contains(err.Error(), "bucket directory not found") {
		t.Errorf("the failure does not say why: %v", err)
	}
}
