package plugins

import "testing"

// Story 54.52: a process's processor use is read from a status row as its memory is: its
// own entry (with its numbered ones), else the component's total; a row from before has none.
func TestMeasure(t *testing.T) {
	d := map[string]any{"cpu_pct": int64(90), "cpu_parts": map[string]any{"plugin-runner/ml-0": int64(40), "plugin-runner/ml-1": int64(30), "plugin-runner/light": int64(20)}}
	if n, scope := measure(d, "cpu_pct", "cpu_parts", "plugin-runner/ml", false); n == nil || *n != 70 || scope != "process" {
		t.Fatalf("numbered processes: %v %q", n, scope)
	}
	if n, _ := measure(d, "cpu_pct", "cpu_parts", "plugin-runner/net", false); n != nil {
		t.Fatalf("a process not reported: %v", *n)
	}
	total := map[string]any{"cpu_pct": float64(12)}
	if n, scope := measure(total, "cpu_pct", "cpu_parts", "pipeline/", false); n == nil || *n != 12 || scope != "component" {
		t.Fatalf("the component's total: %v %q", n, scope)
	}
	if _, scope := measure(total, "cpu_pct", "cpu_parts", "plugins/", true); scope != "process" {
		t.Fatalf("one process: %q", scope)
	}
	if n, _ := measure(map[string]any{"rss_mb": 100}, "cpu_pct", "cpu_parts", "plugins/", true); n != nil {
		t.Fatal("a row with no processor use has one")
	}
}
