package rehearsal

// This file carries no build tag: the cloud unit tier (go test -short ./...) runs the bucket
// step's tests, and the cloudrehearsal scenarios share the step.

// s3Do makes one S3 request to a repository's gateway (path from the gateway's root) and
// answers its HTTP status, 0 when nothing answered.
type s3Do func(method, path string, body ...string) int

// bucketReady makes bucket and proves an object round trip through it, answering false while
// either is not there yet; call it until it answers true. Each call is safe to repeat: a bucket
// that is already there answers 409, and the bucket is never deleted. SeaweedFS's gateway takes
// a new bucket's directory a moment after its metadata, so the first writes into it can fail;
// deleting the bucket then removes its volumes only at the volume server's next heartbeat, and a
// bucket of the same name made in that window leaves the master with no writable volume for the
// collection, every later write failing (db-nodes-migrate on PR 1374). Only the probe object is
// retried.
func bucketReady(do s3Do, bucket string) bool {
	if code := do("PUT", "/"+bucket); code == 0 || (code >= 300 && code != 409) {
		return false
	}
	if do("PUT", "/"+bucket+"/probe", "ok") != 200 || do("GET", "/"+bucket+"/probe") != 200 {
		return false
	}
	return do("DELETE", "/"+bucket+"/probe") < 300
}
