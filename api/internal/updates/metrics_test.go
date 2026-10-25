package updates

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// gatherUpdates gathers m on a registry of its own: every family by name.
func gatherUpdates(t *testing.T, m *Metrics) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(m)
	fams, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, f := range fams {
		out[f.GetName()] = f
	}
	return out
}

// labelValues are a family's values of one label, sorted.
func labelValues(f *dto.MetricFamily, label string) []string {
	var out []string
	for _, m := range f.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() == label {
				out = append(out, l.GetValue())
			}
		}
	}
	sort.Strings(out)
	return out
}

// TC1: every metric of story 35.15 AC1 the API owns is there with its labels, read from
// the database: a second collector (an API that restarted) reads the same numbers (EC1).
func TestUpdateMetricsRegistered(t *testing.T) {
	r := newDriverRig(t)
	ctx := context.Background()
	r.node(t, "n1", "alpha", "linux-amd64", "1.4.0")
	if d, err := r.f.svc.Tick(ctx); err != nil || d.Action != "started" {
		t.Fatalf("tick: %+v %v", d, err)
	}
	run := r.drive(t, 60)
	if run.State != RunDone {
		t.Fatalf("run %s %s", run.State, run.Error)
	}
	// a step with its timing (the machine's journal), the dump its home took, a machine
	// behind the cluster's release, a release that failed verification, the last check
	fin := r.f.now
	if err := r.f.store.ReplaceSteps(ctx, run.ID, HomeHost, stepSeqBase(0, true), []MachineStep{
		{Step: "stage", State: "done", StartedAt: fin.Add(-90 * time.Second), FinishedAt: &fin}}); err != nil {
		t.Fatal(err)
	}
	if err := r.f.store.SetRunBackup(ctx, run.ID, map[string]any{"path": "/b/dump", "bytes": 4096}); err != nil {
		t.Fatal(err)
	}
	r.node(t, "n9", "zulu", "linux-amd64", "1.3.0")
	seed(t, r.f.store, rel("1.6.0"))
	if err := r.f.store.SetVerifyError(ctx, "1.6.0", &VerifyError{Kind: "signature", Detail: "x"}, ""); err != nil {
		t.Fatal(err)
	}
	checked := fin.Add(-time.Hour)
	r.f.store.SaveState(ctx, State{LastCheckAt: &checked})

	for _, attempt := range []string{"first", "after a restart"} {
		fams := gatherUpdates(t, NewMetrics(r.d, func() string { return "1.5.0" }))
		want := map[string]string{
			MetricRuns: "result", MetricRunDuration: "", MetricStepDuration: "step", MetricBackupBytes: "",
			MetricTargets: "state", MetricNodeApply: "", MetricVerifyFailures: "reason",
			MetricMachinesBehind: "", MetricLastCheck: "",
		}
		for name, label := range want {
			f, ok := fams[name]
			if !ok {
				t.Fatalf("%s: %s missing (have %v)", attempt, name, keys(fams))
			}
			if label != "" && len(labelValues(f, label)) == 0 {
				t.Fatalf("%s: %s has no %s label", attempt, name, label)
			}
		}
		if got := labelValues(fams[MetricRuns], "result"); strings.Join(got, ",") != RunDone {
			t.Fatalf("%s: runs by result %v", attempt, got)
		}
		if v := fams[MetricRuns].GetMetric()[0].GetCounter().GetValue(); v != 1 {
			t.Fatalf("%s: runs_total %v", attempt, v)
		}
		if got := labelValues(fams[MetricStepDuration], "step"); !contains(got, "stage") {
			t.Fatalf("%s: steps %v", attempt, got)
		}
		if n := fams[MetricNodeApply].GetMetric()[0].GetHistogram().GetSampleCount(); n != 1 {
			t.Fatalf("%s: node apply samples %d", attempt, n)
		}
		if v := fams[MetricBackupBytes].GetMetric()[0].GetGauge().GetValue(); v != 4096 {
			t.Fatalf("%s: backup bytes %v", attempt, v)
		}
		if v := fams[MetricMachinesBehind].GetMetric()[0].GetGauge().GetValue(); v != 1 {
			t.Fatalf("%s: machines behind %v (zulu on 1.3.0)", attempt, v)
		}
		if got := labelValues(fams[MetricVerifyFailures], "reason"); strings.Join(got, ",") != "signature" {
			t.Fatalf("%s: verify failures %v", attempt, got)
		}
		if v := fams[MetricLastCheck].GetMetric()[0].GetGauge().GetValue(); v != float64(checked.Unix()) {
			t.Fatalf("%s: last check %v", attempt, v)
		}
		healthy := 0.0
		for _, m := range fams[MetricTargets].GetMetric() {
			if m.GetLabel()[0].GetValue() == TargetHealthy {
				healthy = m.GetGauge().GetValue()
			}
		}
		if healthy != 1 || len(fams[MetricTargets].GetMetric()) != len(targetStates) {
			t.Fatalf("%s: targets healthy %v of %d states", attempt, healthy, len(fams[MetricTargets].GetMetric()))
		}
	}
}

func keys(m map[string]*dto.MetricFamily) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
