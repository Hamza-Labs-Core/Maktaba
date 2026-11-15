package rehearsal

// This file carries no build tag: the cloud unit tier (go test -short ./...) runs the bucket
// step's tests, and the cloudrehearsal scenarios share the step.

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// s3Do makes one S3 request to a repository's gateway (path from the gateway's root) and
// answers its HTTP status (0 when nothing answered) and, for an error, why: the S3 error's
// Code and Message, or curl's error.
type s3Do func(method, path string, body ...string) (int, string)

var s3ErrCode, s3ErrMessage = regexp.MustCompile(`<Code>([^<]*)</Code>`), regexp.MustCompile(`<Message>([^<]*)</Message>`)

// s3Why reads an S3 error body down to its Code and Message.
func s3Why(body string) string {
	var why []string
	if m := s3ErrCode.FindStringSubmatch(body); m != nil {
		why = append(why, m[1])
	}
	if m := s3ErrMessage.FindStringSubmatch(body); m != nil {
		why = append(why, m[1])
	}
	if len(why) == 0 {
		body = strings.TrimSpace(body)
		if len(body) > 120 {
			body = body[:120]
		}
		return body
	}
	return strings.Join(why, ": ")
}

// bucketReady makes bucket and proves an object round trip through it. It answers whether
// the bucket serves and, when not, the step that failed with its status and error. Each call
// is safe to repeat: a bucket that is already there answers 409, and the bucket is never
// deleted (a delete drops the bucket's volumes only at the volume server's next heartbeat,
// and a bucket of the same name made before then leaves the master with no writable volume
// for it).
func bucketReady(do s3Do, bucket string) (bool, string) {
	step := func(method, path string, ok func(int) bool, body ...string) (bool, string) {
		code, why := do(method, path, body...)
		if ok(code) {
			return true, ""
		}
		return false, fmt.Sprintf("%s %s: %d %s", method, path, code, why)
	}
	made := func(c int) bool { return c > 0 && (c < 300 || c == 409) }
	is200 := func(c int) bool { return c == 200 }
	if ok, why := step("PUT", "/"+bucket, made); !ok {
		return false, why
	}
	if ok, why := step("PUT", "/"+bucket+"/probe", is200, "ok"); !ok {
		return false, why
	}
	if ok, why := step("GET", "/"+bucket+"/probe", is200); !ok {
		return false, why
	}
	return step("DELETE", "/"+bucket+"/probe", func(c int) bool { return c > 0 && c < 300 })
}

// bucketSteps is how makeBucket waits: how long a bucket may fail before its gateway is
// restarted, how many restarts it takes, and its clock.
type bucketSteps struct {
	stuck    time.Duration
	restarts int
	now      func() time.Time
	sleep    func(time.Duration)
}

// makeBucket calls bucketReady until the bucket serves, logging each change in why it does
// not. A bucket that fails for stuck gets its gateway restarted (restart returns once the
// gateway answers again), at most steps.restarts times. SeaweedFS 3.97's gateway reads a new
// bucket's directory when the filer tells it the bucket was made; when that read comes before
// the directory is there, it caches the bucket as missing for 15 minutes (a quarter of its
// one-hour config cache), and every write into it fails with "bucket directory not found".
// Only a delete of the bucket or a restart of the gateway clears that entry; a restart keeps
// the bucket and its volumes, and the gateway then reads the bucket afresh.
func makeBucket(do s3Do, bucket string, restart func(), logf func(string, ...any), steps bucketSteps) error {
	since, restarts, last, failed := steps.now(), 0, "", false
	for {
		ok, why := bucketReady(do, bucket)
		if ok {
			if failed {
				logf("bucket %s serves", bucket)
			}
			return nil
		}
		failed = true
		if why != last {
			logf("bucket %s not ready: %s", bucket, why)
			last = why
		}
		if steps.now().Sub(since) >= steps.stuck {
			if restarts == steps.restarts {
				return fmt.Errorf("bucket %s did not serve after %d gateway restarts: %s", bucket, restarts, why)
			}
			restarts++
			logf("bucket %s failing for %s: restarting its gateway (%d of %d)", bucket, steps.stuck, restarts, steps.restarts)
			restart()
			since, last = steps.now(), ""
			continue
		}
		steps.sleep(250 * time.Millisecond)
	}
}
