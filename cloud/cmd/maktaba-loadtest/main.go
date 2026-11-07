// maktaba-loadtest measures what one relay carries (Epic 50 story 50.14).
//
//	maktaba-loadtest run --target https://staging.relay.maktaba.dev --db "$STAGING_DB" \
//	    --homes 1000 --homes-step 1000 --players 50 --players-step 50 --bitrate 5M \
//	    --step 3m --node-exporter http://<node>:9100/metrics --plan CPX22 --machine "2 / 4 GB" --out report.json
//	maktaba-loadtest compare --baseline main.json --current this.json
//	maktaba-loadtest sizing --report report.json --doc docs/cloud-sizing.md
//
// run ramps simulated homes (idle tunnels, PING every 25 s) and players (HLS-like segments at a
// bitrate) against the relay, one step at a time, until a stop rule holds for 2 minutes
// (loadtest.DefaultRules); the last step before it is the node's capacity. It refuses any
// target but the rehearsal's containers, this machine and *.maktaba.dev. The load generator
// must not run on the relay's machine (it would measure itself).
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/relay/simhome"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/loadtest"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/seed"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/rehearsal/streamgen"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "sizing":
		err = sizing(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "maktaba-loadtest:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: maktaba-loadtest run|compare|sizing [flags] (see the package comment)")
	os.Exit(2)
}

// parseBits reads 2.5M, 5M, 8M or a number of bits per second.
func parseBits(s string) (int, error) {
	mult := 1.0
	switch {
	case strings.HasSuffix(s, "M"):
		mult, s = 1e6, strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "k"):
		mult, s = 1e3, strings.TrimSuffix(s, "k")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("bitrate %q", s)
	}
	return int(v * mult), nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	target := fs.String("target", "", "the relay's edge: https://staging.relay.maktaba.dev")
	dsn := fs.String("db", os.Getenv("LOADTEST_DB"), "the target cloud's database, to seed the simulated homes (LOADTEST_DB)")
	homes := fs.Int("homes", 1000, "homes at the first step")
	homesStep := fs.Int("homes-step", 1000, "homes added each step")
	players := fs.Int("players", 50, "players at the first step")
	playersStep := fs.Int("players-step", 50, "players added each step")
	bitrate := fs.String("bitrate", "5M", "each player's bitrate (2.5M, 5M, 8M)")
	stepFor := fs.Duration("step", 3*time.Minute, "how long each step runs (more than the rules' 2 minutes)")
	maxSteps := fs.Int("max-steps", 30, "stop after this many steps even if nothing broke")
	nodeExp := fs.String("node-exporter", "", "the relay node's node_exporter /metrics (CPU, memory)")
	container := fs.String("docker", "", "or the relay's container (docker stats), in the rehearsal")
	relayMetrics := fs.String("relay-metrics", "", "the relay's /metrics (tunnels open, to count drops)")
	plan := fs.String("plan", "", "the machine's plan (CPX22), for the sizing table")
	machine := fs.String("machine", "", "its vCPU / RAM, for the sizing table")
	out := fs.String("out", "loadtest-report.json", "where the report goes")
	_ = fs.Parse(args)

	if err := loadtest.CheckTarget(*target); err != nil {
		return err
	}
	bits, err := parseBits(*bitrate)
	if err != nil {
		return err
	}
	var look loadtest.Looker
	switch {
	case *nodeExp != "":
		look = &loadtest.NodeExporter{URL: *nodeExp}
	case *container != "":
		look = &loadtest.Docker{Container: *container}
	default:
		return fmt.Errorf("say how to read the relay's machine: --node-exporter or --docker")
	}
	if *dsn == "" {
		return fmt.Errorf("--db: the target cloud's database, to seed the simulated homes")
	}
	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	api := simhome.API() // the homes' media, from memory: the home side is never the bottleneck
	defer api.Close()
	u, _ := url.Parse(*target)
	ws := "wss://" + u.Host + "/v1/relay/ws"
	if u.Scheme == "http" {
		ws = "ws://" + u.Host + "/v1/relay/ws"
	}
	domain := u.Hostname()

	var running []*simhome.Home
	defer func() {
		for _, h := range running {
			h.Stop()
		}
	}()
	grow := func(n int) ([]string, error) {
		if n <= len(running) {
			return nil, nil
		}
		ids, err := seed.Homes(ctx, db, len(running), n-len(running), "pro")
		if err != nil {
			return nil, fmt.Errorf("seeding homes: %w", err)
		}
		for i := len(running); i < n; i++ {
			_, _, slug := simhome.Creds(i)
			running = append(running, simhome.Start(i, ws, api.URL,
				simhome.Options{ServerID: ids[slug], BaseWait: time.Second, MaxWait: 30 * time.Second, PingEvery: 25 * time.Second}))
		}
		return nil, nil
	}
	slugs := func(n int) []string {
		out := make([]string, 0, n)
		for i := 0; i < n && i < len(running); i++ {
			out = append(out, running[i].Slug)
		}
		return out
	}

	// straight to a simulated home (no relay): what the relay's added latency is measured from
	direct := directTTFB(ctx, api.URL)

	rep := loadtest.Report{Target: *target, Plan: *plan, Machine: *machine, Sha: os.Getenv("GITHUB_SHA"), At: time.Now().UTC()}
	for i := 0; i < *maxSteps && ctx.Err() == nil; i++ {
		step := loadtest.Step{Homes: *homes + i**homesStep, Players: *players + i**playersStep, BitsPerSec: bits}
		if _, err := grow(step.Homes); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "step %d: %d homes, %d players at %s\n", i+1, step.Homes, step.Players, *bitrate)
		stopped, measured := runStep(ctx, look, *relayMetrics, *target, domain, slugs(step.Homes), step, *stepFor, direct)
		measured.Seconds = int(stepFor.Seconds())
		rep.Steps = append(rep.Steps, measured)
		if stopped != "" {
			rep.Stopped = stopped
			break
		}
		good := measured
		rep.LastGood = &good
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Println(loadtest.SizingRow(rep))
	if rep.Stopped != "" {
		fmt.Fprintf(os.Stderr, "stopped by: %s\n", rep.Stopped)
	}
	return nil
}

// runStep plays one step, looking every 5 s; it returns the rule that held for 2 minutes ("" none).
func runStep(ctx context.Context, look loadtest.Looker, relayMetrics, edge, domain string, slugs []string, step loadtest.Step,
	d time.Duration, direct time.Duration) (string, loadtest.Step) {
	sctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	g := &streamgen.Gen{Edge: edge, Domain: domain, Slugs: slugs, Clients: step.Players}
	g.Apply(streamgen.Profile{BitsPerSecond: step.BitsPerSec, Segment: 4 * time.Second})
	done := make(chan struct{})
	go func() { g.Run(sctx); close(done) }()
	w := loadtest.Watch{Rules: loadtest.DefaultRules}
	tk := time.NewTicker(5 * time.Second)
	defer tk.Stop()
	start := time.Now()
	var stopped string
	var last loadtest.Machine
loop:
	for {
		select {
		case <-sctx.Done():
			break loop
		case now := <-tk.C:
			m, err := look.Look(sctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "look:", err)
				continue
			}
			last = m
			st := g.Stats()
			dropped := 0
			if relayMetrics != "" {
				if pm, err := loadtest.Scrape(sctx, relayMetrics); err == nil {
					if open := int(pm["maktaba_relay_active_tunnels"]); open < len(slugs) && now.Sub(start) > time.Minute {
						dropped = len(slugs) - open
					}
				}
			}
			added := streamgen.Percentile(st.TTFB, 0.95) - direct
			if rule := w.Add(loadtest.Sample{At: now, CPU: m.CPU, Mem: m.Mem, AddedP95: added, StallRate: st.StallRate(), Dropped: dropped}); rule != "" {
				stopped = rule
				cancel()
				break loop
			}
		}
	}
	<-done
	st := g.Stats()
	secs := time.Since(start).Seconds()
	step.Mbit = float64(st.Bytes) * 8 / 1e6 / secs
	step.CPU, step.Mem = last.CPU, last.Mem
	step.StallRate = st.StallRate()
	step.AddedP95MS = float64(streamgen.Percentile(st.TTFB, 0.95)-direct) / float64(time.Millisecond)
	step.StoppedBy = stopped
	return stopped, step
}

// directTTFB is the p95 time to first byte straight to a simulated home (20 small asks).
func directTTFB(ctx context.Context, api string) time.Duration {
	g := &streamgen.Gen{}
	var ds []time.Duration
	for range 20 {
		_, first, err := streamgen.GetTimed(ctx, g.Client(), api, "", "/stream?bytes=1024&ms=0")
		if err == nil && first > 0 {
			ds = append(ds, first)
		}
	}
	return streamgen.Percentile(ds, 0.95)
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func compare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	basePath := fs.String("baseline", "", "main's last load-small (baseline JSON); missing: this run is the first")
	curPath := fs.String("current", "", "this run's load-small (baseline JSON)")
	_ = fs.Parse(args)
	var base, cur loadtest.Baseline
	if err := readJSON(*curPath, &cur); err != nil {
		return err
	}
	if err := readJSON(*basePath, &base); err != nil {
		fmt.Printf("no baseline from main yet (%v): this run becomes it\n", err)
		return nil
	}
	ok, rep := loadtest.Compare(base, cur, loadtest.Regressed)
	fmt.Print(rep)
	if !ok {
		return fmt.Errorf("the relay got more than %.0f%% worse", loadtest.Regressed*100)
	}
	return nil
}

func sizing(args []string) error {
	fs := flag.NewFlagSet("sizing", flag.ExitOnError)
	repPath := fs.String("report", "", "a run's report")
	doc := fs.String("doc", "docs/cloud-sizing.md", "the sizing table")
	_ = fs.Parse(args)
	var r loadtest.Report
	if err := readJSON(*repPath, &r); err != nil {
		return err
	}
	if r.Plan == "" || r.LastGood == nil {
		return fmt.Errorf("the report has no plan or no good step")
	}
	b, err := os.ReadFile(*doc)
	if err != nil {
		return err
	}
	updated, err := loadtest.UpdateSizing(string(b), r.Plan, loadtest.SizingRow(r))
	if err != nil {
		return err
	}
	return os.WriteFile(*doc, []byte(updated), 0o644)
}
