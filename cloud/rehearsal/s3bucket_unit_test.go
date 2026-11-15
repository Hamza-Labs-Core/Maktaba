package rehearsal

import "testing"

// fakeSeaweed answers like SeaweedFS's S3 gateway: a new bucket takes lag calls before objects
// can be written into it, and a bucket deleted and made again wedges (every write fails).
type fakeSeaweed struct {
	bucket          bool
	lag             int
	wedged          bool
	bucketDeletes   int
	object, objects int
	down            int // calls answered with nothing before the gateway serves
}

func (f *fakeSeaweed) do(method, path string, _ ...string) int {
	if f.down > 0 {
		f.down--
		return 0
	}
	switch {
	case method == "PUT" && path == "/b":
		if f.bucket {
			return 409
		}
		if f.bucketDeletes > 0 {
			f.wedged = true
		}
		f.bucket = true
		return 200
	case method == "DELETE" && path == "/b":
		f.bucket = false
		f.bucketDeletes++
		return 204
	case method == "PUT" && path == "/b/probe":
		if !f.bucket {
			return 404
		}
		if f.wedged {
			return 500
		}
		if f.lag > 0 {
			f.lag--
			return 500
		}
		f.object = 1
		f.objects++
		return 200
	case method == "GET" && path == "/b/probe":
		if f.object == 0 {
			return 404
		}
		return 200
	case method == "DELETE" && path == "/b/probe":
		f.object = 0
		return 204
	}
	return 400
}

// until calls ready up to n times and answers whether it said true.
func until(n int, ready func() bool) bool {
	for range n {
		if ready() {
			return true
		}
	}
	return false
}

func TestBucketReadyWaitsOutTheNewBucketsLag(t *testing.T) {
	f := &fakeSeaweed{lag: 5}
	if !until(20, func() bool { return bucketReady(f.do, "b") }) {
		t.Fatal("the bucket never became ready")
	}
	if f.bucketDeletes != 0 {
		t.Errorf("the bucket was deleted %d times; deleting it wedges SeaweedFS", f.bucketDeletes)
	}
	if f.object != 0 {
		t.Error("the probe object was left in the bucket")
	}
}

func TestBucketReadyTakesABucketThatIsThere(t *testing.T) {
	f := &fakeSeaweed{bucket: true}
	if !bucketReady(f.do, "b") {
		t.Fatal("a bucket that is already there (409) was not taken")
	}
}

func TestBucketReadyWaitsForTheGateway(t *testing.T) {
	f := &fakeSeaweed{down: 3}
	if bucketReady(f.do, "b") {
		t.Fatal("ready while the gateway answered nothing")
	}
	if !until(5, func() bool { return bucketReady(f.do, "b") }) {
		t.Fatal("the bucket never became ready once the gateway served")
	}
}

func TestBucketReadyIsNotReadyUntilAWriteReadsBack(t *testing.T) {
	f := &fakeSeaweed{wedged: true}
	if until(10, func() bool { return bucketReady(f.do, "b") }) {
		t.Fatal("ready though no object could be written")
	}
}
