//go:build cloudrehearsal

package rehearsal

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// tunnel, written as load-small-baseline.json; the workflow compares it with main's last
// (maktaba-loadtest compare) and fails at 20 % worse.
func loadSmall(t *testing.T, c *cluster) Result {
	n := envInt("REHEARSAL_HOMES", 1000)
	players := envInt("REHEARSAL_STREAMS", 50)
	secs := envInt("REHEARSAL_LOAD_SECONDS", 300)
	slugs := c.homesOnline(n)

	out, err := exec.Command("docker", "info", "--format", "{{.NCPU}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	cpus, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	meter := loadtest.Meter{CPUs: cpus, Looker: sumLooker{
		&loadtest.Docker{Container: c.project + "-relay-blue-1"},
		&loadtest.Docker{Container: c.project + "-relay-green-1"},
	}}
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
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case now := <-tk.C:
			if _, err := meter.Tick(ctx, now); err != nil && ctx.Err() == nil {
				t.Logf("docker stats: %v", err)
			}
		}
	}
	<-done
	took := time.Since(start)
	st := g.Stats()
	mbit := float64(st.Bytes) * 8 / 1e6
	if mbit == 0 {
		t.Fatalf("nothing was relayed: %+v", st)
	}
	base := loadtest.Baseline{Sha: os.Getenv("GITHUB_SHA"), CPUSecondsPerMbit: meter.CPUSeconds() / mbit,
		MemBytesPerTunnel: meter.PeakMem() / float64(n), Mbit: mbit, Tunnels: n}
	b, _ := json.MarshalIndent(base, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir(t), "load-small-baseline.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	rate := mbit / took.Seconds()
	t.Logf("%d homes, %d players: %.1f Mbit/s relayed for %s; %.4f CPU-seconds per Mbit, %.0f bytes per tunnel; stalls %.2f%%, TTFB p95 %s",
		n, players, rate, took.Round(time.Second), base.CPUSecondsPerMbit, base.MemBytesPerTunnel, st.StallRate()*100, st.TTFBP95)
	return Result{FailedRequests: st.Failed, Requests: st.OK + st.Failed, Notes: []string{
		fmt.Sprintf("%.1f Mbit/s; %.4f CPU-s/Mbit; %.0f B/tunnel; stalls %.2f%% (the runner shares its CPUs with the cluster: a regression check, not a capacity)",
			rate, base.CPUSecondsPerMbit, base.MemBytesPerTunnel, st.StallRate()*100)}}
}
