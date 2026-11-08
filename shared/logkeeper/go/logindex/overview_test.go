package logindex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// overviewFleet records two scrapes a minute apart from 60 machines (n00…n59) of 10
// components each, with a few in trouble: n07 has 3 components down, n33 one that went from
// running to failed between the scrapes, n12 and n41 the least disk free, n20 the highest load.
// n07's c1 restarted twice between the scrapes.
func overviewFleet(t *testing.T) (*Index, context.Context) {
	t.Helper()
	root, now := t.TempDir(), t0
	ix := open(t, root, &now)
	ctx := context.Background()
	for scrape := 0; scrape < 2; scrape++ {
		var b strings.Builder
		for i := 0; i < 60; i++ {
			m := fmt.Sprintf("n%02d", i)
			disk, load := 1e12, 1.0
			switch m {
			case "n12":
				disk = 1e9
			case "n41":
				disk = 5e9
			case "n20":
				load = 30
			}
			fmt.Fprintf(&b, "maktaba_host_load1{machine=%q} %g\nmaktaba_host_cpus{machine=%q} 4\n", m, load, m)
			fmt.Fprintf(&b, "maktaba_host_memory_bytes{machine=%q} 8e9\nmaktaba_host_memory_free_bytes{machine=%q} %g\n", m, m, 4e9-float64(i))
			fmt.Fprintf(&b, "maktaba_host_disk_free_bytes{machine=%q} %g\n", m, disk)
			for c := 0; c < 10; c++ {
				phase, up := "running", 1
				if m == "n07" && c < 3 {
					phase, up = "failed", 0
				}
				if m == "n33" && c == 0 && scrape == 1 {
					phase, up = "failed", 0
				}
				fmt.Fprintf(&b, "maktaba_component_up{id=\"c%d\",machine=%q,phase=%q} %d\n", c, m, phase, up)
			}
			if m == "n07" {
				fmt.Fprintf(&b, "maktaba_component_restarts_total{id=\"c1\",machine=%q} %d\n", m, 1+2*scrape)
			}
		}
		now = now.Add(time.Minute)
		if err := ix.Record(ctx, parseMetrics(strings.NewReader(b.String())), now); err != nil {
			t.Fatal(err)
		}
	}
	return ix, ctx
}

func lineOf(doc BoardDoc, name, labels string) (Series, bool) {
	for _, s := range doc.Series {
		if s.Name == name && s.Labels == labels {
			return s, true
		}
	}
	return Series{}, false
}

func last(s Series) float64 {
	if len(s.Points) == 0 {
		return -1
	}
	return s.Points[len(s.Points)-1][1]
}

// Story 53.12: with many machines the Overview keeps within MaxSeries. It draws totals across
// the machines and the TopMachines most in need of attention, and names how many machines
// there are, which are shown and every machine (the store keeps at most the label cap of 50
// machines, so 50 of the 60).
func TestOverviewManyMachines(t *testing.T) {
	ix, ctx := overviewFleet(t)
	doc, err := ix.Board(ctx, "overview", "1h")
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Series) > MaxSeries || doc.Omitted != 0 {
		t.Fatalf("%d lines, %d left out; want all within %d", len(doc.Series), doc.Omitted, MaxSeries)
	}
	for _, s := range doc.Series {
		if len(s.Points) > MaxPoints {
			t.Errorf("%s{%s}: %d points", s.Name, s.Labels, len(s.Points))
		}
	}
	mc := doc.Machines
	if mc == nil || mc.Total != 50 || len(mc.All) != 50 || mc.All[0] != "n00" || mc.All[49] != "n49" {
		t.Fatalf("machines: %+v, want the 50 the store keeps", mc)
	}
	if got := strings.Join(mc.Shown, ","); got != "n07,n33,n12,n41,n20" {
		t.Fatalf("shown %s, want the most components down, then least disk, then highest load", got)
	}
	for name, want := range map[string]float64{
		MachinesComponentsUp:   50*10 - 4,
		MachinesComponentsDown: 4,
		MachinesDiskFreeMin:    1e9,
		MachinesMemoryFreeMin:  4e9 - 49,
		MachinesLoadMax:        30,
		MachinesRestarts:       2,
	} {
		s, ok := lineOf(doc, name, "")
		if !ok || last(s) != want {
			t.Errorf("%s: %v (found %v), want %v", name, last(s), ok, want)
		}
	}
	per := map[string][]string{}
	for _, s := range doc.Series {
		if m := labelValue(s.Labels, "machine"); m != "" {
			per[s.Name] = append(per[s.Name], m)
		}
	}
	for _, name := range []string{ComponentDown, "maktaba_host_load1", "maktaba_host_disk_free_bytes", "maktaba_host_memory_free_bytes"} {
		if got := strings.Join(per[name], ","); got != "n07,n33,n12,n41,n20" {
			t.Errorf("%s: lines for %s, want the shown machines in their order", name, got)
		}
	}
	if len(per["maktaba_component_up"]) != 0 || len(per["maktaba_host_cpus"]) != 0 {
		t.Errorf("per-component and CPU lines belong to one machine's view: %v", per)
	}
	if s, _ := lineOf(doc, ComponentDown, "machine=n07"); last(s) != 3 {
		t.Errorf("n07 down: %v, want 3", last(s))
	}
}

// Story 53.12: machine= answers one machine's lines: its components up and down, its host
// lines, a line for each component that was not up all the time and each that restarted;
// nothing of the other machines or the home's own series.
func TestOverviewOneMachine(t *testing.T) {
	ix, ctx := overviewFleet(t)
	doc, err := ix.BoardOf(ctx, "overview", "1h", "n07")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Machine != "n07" || doc.Machines == nil || doc.Machines.Total != 50 || strings.Join(doc.Machines.Shown, ",") != "n07" {
		t.Fatalf("machine %q, machines %+v", doc.Machine, doc.Machines)
	}
	var names []string
	for _, s := range doc.Series {
		if !strings.Contains(","+s.Labels+",", ",machine=n07,") {
			t.Errorf("%s{%s}: not n07's", s.Name, s.Labels)
		}
		names = append(names, s.Name+"{"+s.Labels+"}")
	}
	want := []string{
		"maktaba_component_up{machine=n07}", "maktaba_component_down{machine=n07}",
		"maktaba_host_load1{machine=n07}", "maktaba_host_cpus{machine=n07}", "maktaba_host_memory_bytes{machine=n07}",
		"maktaba_host_memory_free_bytes{machine=n07}", "maktaba_host_disk_free_bytes{machine=n07}",
		"maktaba_component_up{id=c0,machine=n07}", "maktaba_component_up{id=c1,machine=n07}",
		"maktaba_component_up{id=c2,machine=n07}", "maktaba_component_restarts_total{id=c1,machine=n07}",
	}
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Fatalf("lines:\n%s\nwant:\n%s", strings.Join(names, "\n"), strings.Join(want, "\n"))
	}
	if s, _ := lineOf(doc, "maktaba_component_up", "machine=n07"); last(s) != 7 {
		t.Errorf("n07 up: %v, want 7", last(s))
	}
	// a machine the store has not seen: no lines, the machines still named
	none, err := ix.BoardOf(ctx, "overview", "1h", "n59")
	if err != nil || len(none.Series) != 0 || none.Machines == nil || none.Machines.Total != 50 {
		t.Fatalf("n59: %v %d lines, machines %+v", err, len(none.Series), none.Machines)
	}
	if _, err := ix.BoardOf(ctx, "overview", "1h", strings.Repeat("x", maxMachineID+1)); err != ErrBoard {
		t.Errorf("a machine id past %d bytes: %v, want ErrBoard", maxMachineID, err)
	}
	// machine= is the Overview's; another board ignores it
	st, err := ix.BoardOf(ctx, "storage", "1h", "n07")
	if err != nil || st.Machine != "" || st.Machines != nil {
		t.Errorf("storage with machine=: %v %q %+v", err, st.Machine, st.Machines)
	}
}

// Story 53.12: a component whose phase changed inside one bucket is still one component (its
// value averaged over the bucket), so the machine's components down are not counted twice.
func TestOverviewPhaseChangeCountsOnce(t *testing.T) {
	ix, ctx := overviewFleet(t)
	// 7 d: a bucket of 6 minutes holds both scrapes (10:16 and 10:17)
	doc, err := ix.BoardOf(ctx, "overview", "7d", "n33")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Step != 360 {
		t.Fatalf("step %d, want 360", doc.Step)
	}
	up, _ := lineOf(doc, "maktaba_component_up", "machine=n33")
	down, _ := lineOf(doc, ComponentDown, "machine=n33")
	if len(up.Points) != 1 || last(up) != 9.5 || last(down) != 0.5 {
		t.Fatalf("n33 up %v, down %v; want 9.5 and 0.5 in one bucket", up.Points, down.Points)
	}
}

// Story 53.12: the log index's /dashboards route passes machine= on.
func TestOverviewRouteTakesMachine(t *testing.T) {
	ix, _ := overviewFleet(t)
	srv := httptest.NewServer(ix.Handler(nil))
	defer srv.Close()
	r, err := http.Get(srv.URL + "/dashboards/overview?range=1h&machine=n12")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("%v %v", err, r)
	}
	defer r.Body.Close()
	var d BoardDoc
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil || d.Machine != "n12" || d.Machines == nil {
		t.Fatalf("%v %+v", err, d)
	}
}

// smallFleet records one scrape from 8 machines (n00…n07) of 4 components each: n03 has 3
// components down, n05 the least disk free; the rest are alike.
func smallFleet(t *testing.T) (*Index, context.Context) {
	t.Helper()
	root, now := t.TempDir(), t0
	ix := open(t, root, &now)
	ctx := context.Background()
	var b strings.Builder
	for i := 0; i < 8; i++ {
		m := fmt.Sprintf("n%02d", i)
		disk := 1e12
		if m == "n05" {
			disk = 1e9
		}
		fmt.Fprintf(&b, "maktaba_host_load1{machine=%q} 1\nmaktaba_host_cpus{machine=%q} 4\n", m, m)
		fmt.Fprintf(&b, "maktaba_host_memory_free_bytes{machine=%q} 4e9\nmaktaba_host_disk_free_bytes{machine=%q} %g\n", m, m, disk)
		for c := 0; c < 4; c++ {
			phase, up := "running", 1
			if m == "n03" && c < 3 {
				phase, up = "failed", 0
			}
			fmt.Fprintf(&b, "maktaba_component_up{id=\"c%d\",machine=%q,phase=%q} %d\n", c, m, phase, up)
		}
	}
	now = now.Add(time.Minute)
	if err := ix.Record(ctx, parseMetrics(strings.NewReader(b.String())), now); err != nil {
		t.Fatal(err)
	}
	return ix, ctx
}

// Story 53.12: a joined machine that is offline sends no fresh values, so by its values it
// ranked last (n06 here, all well) or was missing (n09, never in the store). Named offline,
// both rank first among the machines shown, are counted and listed, and keep the bounds.
func TestOverviewOfflineFirst(t *testing.T) {
	ix, ctx := smallFleet(t)
	doc, err := ix.BoardFor(ctx, "overview", "1h", "", []string{"n09", "n06"})
	if err != nil {
		t.Fatal(err)
	}
	mc := doc.Machines
	if mc == nil || mc.Total != 9 || len(mc.All) != 9 || strings.Join(mc.Offline, ",") != "n06,n09" {
		t.Fatalf("machines: %+v, want the 8 in the store and n09, n06 and n09 offline", mc)
	}
	if got := strings.Join(mc.Shown, ","); got != "n06,n09,n03,n05,n00" {
		t.Fatalf("shown %s, want the offline machines first, then the most components down, then least disk", got)
	}
	if len(doc.Series) > MaxSeries || doc.Omitted != 0 {
		t.Fatalf("%d lines, %d left out", len(doc.Series), doc.Omitted)
	}
	for _, s := range doc.Series {
		if len(s.Points) > MaxPoints {
			t.Errorf("%s{%s}: %d points", s.Name, s.Labels, len(s.Points))
		}
		if labelValue(s.Labels, "machine") == "n09" {
			t.Errorf("n09 has no lines in the store, yet %s{%s}", s.Name, s.Labels)
		}
	}
	if _, ok := lineOf(doc, "maktaba_host_load1", "machine=n06"); !ok {
		t.Error("n06's last values are still drawn")
	}
	// none offline: the list is there and empty (a client reads it as is)
	plain, err := ix.Board(ctx, "overview", "1h")
	if err != nil || plain.Machines == nil || plain.Machines.Offline == nil || len(plain.Machines.Offline) != 0 ||
		plain.Machines.Shown[0] != "n03" {
		t.Fatalf("%v %+v", err, plain.Machines)
	}
	// one offline machine on its own: named offline, its lines as they were
	one, err := ix.BoardFor(ctx, "overview", "1h", "n06", []string{"n06"})
	if err != nil || strings.Join(one.Machines.Shown, ",") != "n06" || strings.Join(one.Machines.Offline, ",") != "n06" ||
		len(one.Series) == 0 {
		t.Fatalf("n06 alone: %v %+v, %d lines", err, one.Machines, len(one.Series))
	}
	// the bounds: too many, or an id too long
	many := make([]string, MaxOffline+1)
	for i := range many {
		many[i] = fmt.Sprintf("m%d", i)
	}
	if _, err := ix.BoardFor(ctx, "overview", "1h", "", many); err != ErrBoard {
		t.Errorf("%d offline: %v, want ErrBoard", len(many), err)
	}
	if _, err := ix.BoardFor(ctx, "overview", "1h", "", []string{strings.Repeat("x", maxMachineID+1)}); err != ErrBoard {
		t.Errorf("an offline id past %d bytes: %v, want ErrBoard", maxMachineID, err)
	}
	// offline= is the Overview's; another board ignores it
	if st, err := ix.BoardFor(ctx, "storage", "1h", "", []string{"n06"}); err != nil || st.Machines != nil {
		t.Errorf("storage with offline=: %v %+v", err, st.Machines)
	}
}

// Story 53.12: the log index's /dashboards route passes offline= on.
func TestOverviewRouteTakesOffline(t *testing.T) {
	ix, _ := smallFleet(t)
	srv := httptest.NewServer(ix.Handler(nil))
	defer srv.Close()
	r, err := http.Get(srv.URL + "/dashboards/overview?range=1h&offline=n06,n09")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("%v %v", err, r)
	}
	defer r.Body.Close()
	var d BoardDoc
	if err := json.NewDecoder(r.Body).Decode(&d); err != nil || d.Machines == nil ||
		strings.Join(d.Machines.Offline, ",") != "n06,n09" || d.Machines.Shown[0] != "n06" {
		t.Fatalf("%v %+v", err, d.Machines)
	}
}
