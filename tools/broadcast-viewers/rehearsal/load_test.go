package rehearsal

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestBucketQuantile(t *testing.T) {
	bounds := []float64{10, 100, 1000}
	cases := []struct {
		counts []int
		q      float64
		want   float64
	}{
		{[]int{0, 0, 0, 0}, 0.95, 0},
		{[]int{100, 0, 0, 0}, 0.95, 10},
		{[]int{95, 5, 0, 0}, 0.95, 10},  // the 95th of 100 is still ≤ 10
		{[]int{94, 6, 0, 0}, 0.95, 100}, // the 95th is in the next bucket
		{[]int{90, 0, 5, 5}, 0.95, 1000},
		{[]int{90, 0, 4, 6}, 0.95, math.Inf(1)}, // past the last bound
		{[]int{1}, 0, 10},
	}
	for _, c := range cases {
		if got := BucketQuantile(c.counts, bounds, c.q); got != c.want {
			t.Errorf("BucketQuantile(%v, %.2f) = %v, want %v", c.counts, c.q, got, c.want)
		}
	}
}

func TestSegAnswerBoundsMatchTheEdge(t *testing.T) {
	// the edge's buckets (entry.ts) and the rehearsal's reading of them are one list
	raw, err := os.ReadFile("../../rehearsal-cloud/broadcast/edge/entry.ts")
	if err != nil {
		t.Fatal(err)
	}
	const decl = "const SEG_ANSWER_BOUNDS_MS = ["
	src := string(raw)
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatal("entry.ts has no SEG_ANSWER_BOUNDS_MS")
	}
	list := src[i+len(decl):]
	list = list[:strings.IndexByte(list, ']')]
	var got, want []string
	for _, f := range strings.Split(list, ",") {
		got = append(got, strings.TrimSpace(f))
	}
	for _, b := range SegAnswerBoundsMS {
		want = append(want, strconv.FormatFloat(b, 'f', -1, 64))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entry.ts buckets %v, SegAnswerBoundsMS %v", got, want)
	}
}

func TestCgroupCPUUsec(t *testing.T) {
	n, ok := CgroupCPUUsec("usage_usec 3561343\nuser_usec 2844233\nsystem_usec 717110\n")
	if !ok || n != 3561343 {
		t.Fatalf("got %d %v", n, ok)
	}
	if _, ok := CgroupCPUUsec("user_usec 1\n"); ok {
		t.Fatal("no usage_usec read as one")
	}
}

func TestHostShare(t *testing.T) {
	// user nice system idle iowait irq softirq steal guest guest_nice
	a, ok := ParseProcStat("cpu  100 0 100 700 0 0 0 100 50 0\ncpu0 1 2 3\n")
	if !ok || a.Total != 1000 || a.Idle != 700 || a.Steal != 100 {
		t.Fatalf("a = %+v %v", a, ok)
	}
	b, _ := ParseProcStat("cpu  400 0 200 1100 100 0 0 200 80 0\n")
	busy, steal := HostShare(a, b)
	// Δtotal 1000, Δidle 500: busy 50 %, Δsteal 100: 10 %
	if busy != 50 || steal != 10 {
		t.Fatalf("busy %.1f steal %.1f", busy, steal)
	}
	if _, ok := ParseProcStat("intr 1 2 3"); ok {
		t.Fatal("a non-cpu line read")
	}
}

func TestCores(t *testing.T) {
	if got := Cores(30_000_000, 60); got != 0.5 {
		t.Fatalf("got %v", got)
	}
	if got := Cores(1, 0); got != 0 {
		t.Fatalf("got %v", got)
	}
}
