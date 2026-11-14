//go:build cloudrehearsal

package rehearsal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/loadtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/streamgen"
)

// sumLooker adds the machine shares of several containers (both relay colours).
type sumLooker []loadtest.Looker

func (s sumLooker) Look(ctx context.Context) (loadtest.Machine, error) {
	var out loadtest.Machine
	for _, l := range s {
		m, err := l.Look(ctx)
		if err != nil {
			return out, err
		}
		out.CPU += m.CPU
		out.Mem += m.Mem
		out.MemBytes += m.MemBytes
	}
	return out, nil
}

// load-small (story 50.14 §3, T04): 1,000 homes holding their tunnels and 50 players at
// 5 Mbit/s for 5 minutes through the one-node cluster. CI can't measure a Hetzner machine, so
// this measures what regresses: the relay's CPU-seconds per Mbit relayed and its memory per
// tunnel, written as load-small-baseline.json. The workflow runs it twice on one runner, with
// the base commit's relay and with this commit's, compares the two (maktaba-loadtest compare)
// and fails at 20 % worse: a figure from another run is another machine (#832).
//
// The CPU is the relay containers' own CPU time (their cgroups' counters, read at each round's
// ends), per Mbit relayed in that round, over REHEARSAL_LOAD_ROUNDS rounds; the median is the
// figure. A single figure integrated from docker stats' instants swung 1.8 times between runs
// of the same code on one runner shared with the cluster (#808).
//
// The memory is the relays' Go live heap (maktaba_go_heap_live_bytes on their /metrics), read
// once before the homes connect and every 5 s under the load: the median of each round's
// samples, the median of the rounds, less the heap before, over the tunnels
// (loadtest.HeapRounds). docker stats' peak, one sample of a sawtooth, read 254k to 327k bytes
// per tunnel on the same code and +28 % on a relay that held 70 B more per tunnel; it is still
// written, for reference. A base whose relay serves no live heap (a build from before the
// series) writes no heap figure, and compare says it did not compare the memory.
func loadSmall(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 1000)
	players := envInt("REHEARSAL_STREAMS", 50)
	secs := envInt("REHEARSAL_LOAD_SECONDS", 300)
	rounds := max(envInt("REHEARSAL_LOAD_ROUNDS", 5), 1)
	ports := portsFor(c.project)
	heap := loadtest.LiveHeap{Host: "relay.test", URLs: []string{
		fmt.Sprintf("http://127.0.0.1:%d/metrics", ports[1]+500), fmt.Sprintf("http://127.0.0.1:%d/metrics", ports[1]+501),
	}}
	var heapRounds loadtest.HeapRounds
	heapOK := true
	if b, err := heap.Bytes(context.Background()); err != nil {
		if !errors.Is(err, loadtest.ErrNoLiveHeap) {
			t.Fatalf("the relays' live heap: %v", err)
		}
		heapOK = false
		t.Logf("%v: no heap figure (memory from docker stats' peak only)", err)
	} else {
		heapRounds.Before = b
	}
	diagRelays(t, heap, "before", true)
	slugs := c.homesOnline(n)
	diagRelays(t, heap, "homes", true)

	out, err := exec.Command("docker", "info", "--format", "{{.NCPU}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	cpus, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	relays := []string{c.project + "-relay-blue-1", c.project + "-relay-green-1"}
	// memory: docker stats' peak, as before
	meter := loadtest.Meter{CPUs: cpus, Looker: sumLooker{
		&loadtest.Docker{Container: relays[0]}, &loadtest.Docker{Container: relays[1]},
	}}
	relayCPU := func() float64 {
		var sum float64
		for _, r := range relays {
			s, err := loadtest.CgroupCPU{Container: r}.Seconds(context.Background())
			if err != nil {
				t.Fatalf("the relay's CPU time: %v", err)
			}
			sum += s
		}
		return sum
	}
	g := &streamgen.Gen{Edge: c.edge, Domain: "relay.test", Slugs: shuffled(slugs), Clients: players}
	g.Apply(streamgen.Profile{BitsPerSecond: 5_000_000, Segment: 4 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()
	done := make(chan struct{})
	start := time.Now()
	go func() { g.Run(ctx); close(done) }()
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	if _, err := meter.Tick(ctx, start); err != nil {
		t.Fatalf("docker stats: %v", err)
	}
	roundLen := time.Duration(secs) * time.Second / time.Duration(rounds)
	nextRound := start.Add(roundLen)
	cpu0, bytes0 := relayCPU(), g.Stats().Bytes
	var perRound []float64
	endRound := func() {
		cpu1, bytes1 := relayCPU(), g.Stats().Bytes
		if mb := float64(bytes1-bytes0) * 8 / 1e6; mb > 0 {
			perRound = append(perRound, (cpu1-cpu0)/mb)
		}
		cpu0, bytes0 = cpu1, bytes1
		heapRounds.EndRound()
		diagRelays(t, heap, fmt.Sprintf("round%d", len(perRound)), true)
	}
	sampleHeap := func(ctx context.Context) {
		if !heapOK {
			return
		}
		b, err := heap.Bytes(ctx)
		switch {
		case err == nil:
			heapRounds.Add(b)
			diagRelays(t, heap, "sample", false)
		case ctx.Err() == nil:
			t.Logf("the relays' live heap: %v", err)
		}
	}
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case now := <-tk.C:
			if _, err := meter.Tick(ctx, now); err != nil && ctx.Err() == nil {
				t.Logf("docker stats: %v", err)
			}
			sampleHeap(ctx)
			if !now.Before(nextRound) && len(perRound) < rounds-1 {
				endRound()
				nextRound = nextRound.Add(roundLen)
			}
		}
	}
	<-done
	diagRelays(t, heap, "end", true)
	endRound()
	took := time.Since(start)
	st := g.Stats()
	mbit := float64(st.Bytes) * 8 / 1e6
	if mbit == 0 || len(perRound) == 0 {
		t.Fatalf("nothing was relayed: %+v", st)
	}
	base := loadtest.Baseline{Sha: os.Getenv("GITHUB_SHA"), Method: loadtest.Method,
		CPUSecondsPerMbit: loadtest.Median(perRound), CPURounds: perRound,
		MemBytesPerTunnel: meter.PeakMem() / float64(n), Mbit: mbit, Tunnels: n}
	if heapOK {
		base.HeapBytesPerTunnel, base.HeapRounds = heapRounds.PerTunnel(n)
		if base.HeapBytesPerTunnel <= 0 {
			t.Fatalf("the live heap added nothing per tunnel: before %.0f, rounds %.0f", heapRounds.Before, base.HeapRounds)
		}
		base.MemMethod = loadtest.MemMethod
		t.Logf("live heap bytes per tunnel by round: %.0f (median %.0f; %.0f before the homes)",
			base.HeapRounds, base.HeapBytesPerTunnel, heapRounds.Before)
	}
	b, _ := json.MarshalIndent(base, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir(t), "load-small-baseline.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("CPU-seconds per Mbit by round: %.4g (median %.4g; docker stats' sampled estimate %.4g)",
		perRound, base.CPUSecondsPerMbit, meter.CPUSeconds()/mbit)
	rate := mbit / took.Seconds()
	t.Logf("%d homes, %d players: %.1f Mbit/s relayed for %s; %.4f CPU-seconds per Mbit, %.0f live heap and %.0f peak container bytes per tunnel; stalls %.2f%%, TTFB p95 %s",
		n, players, rate, took.Round(time.Second), base.CPUSecondsPerMbit, base.HeapBytesPerTunnel, base.MemBytesPerTunnel, st.StallRate()*100, st.TTFBP95)
	return Result{FailedRequests: st.Failed, Requests: st.OK + st.Failed, Notes: []string{
		fmt.Sprintf("%.1f Mbit/s; %.4f CPU-s/Mbit; %.0f B/tunnel live heap (%.0f peak container); stalls %.2f%% (the runner shares its CPUs with the cluster: a regression check, not a capacity)",
			rate, base.CPUSecondsPerMbit, base.HeapBytesPerTunnel, base.MemBytesPerTunnel, st.StallRate()*100)}}
}

// diagRelays: TEMP diagnostic (reverted before merge): each relay's heap, tunnels and GC state,
// and with prof its heap profiles (as the last GC left it, and after a forced one).
func diagRelays(t *testing.T, h loadtest.LiveHeap, label string, prof bool) {
	for i, u := range h.URLs {
		get := func(url string) []byte {
			req, _ := http.NewRequest(http.MethodGet, url, nil)
			req.Host = h.Host
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Logf("diag %s: %v", url, err)
				return nil
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return b
		}
		m := loadtest.ParseProm(bytes.NewReader(get(u)))
		var keys []string
		for k := range m {
			if strings.HasPrefix(k, "diag") || k == "maktaba_relay_active_tunnels" || k == loadtest.LiveHeapSeries {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			fmt.Fprintf(&sb, " %s=%.0f", strings.TrimPrefix(k, "diag_"), m[k])
		}
		t.Logf("DIAG %s relay%d:%s", label, i, sb.String())
		if prof {
			base := strings.TrimSuffix(u, "/metrics") + "/debug/diagheap"
			_ = os.WriteFile(filepath.Join(outDir(t), fmt.Sprintf("heap-%s-relay%d.pb.gz", label, i)), get(base), 0o644)
			_ = os.WriteFile(filepath.Join(outDir(t), fmt.Sprintf("heap-%s-relay%d-gc.pb.gz", label, i)), get(base+"?gc=1"), 0o644)
			t.Logf("DIAG %s relay%d live after forced GC: %s", label, i, get(base+"?live=1"))
		}
	}
}
