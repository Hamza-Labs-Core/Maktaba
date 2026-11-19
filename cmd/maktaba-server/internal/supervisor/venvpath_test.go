package supervisor

import (
	"testing"
)

// pythonModule is the module p's command runs with `python -m` ("" none): the runner's
// sandbox launcher on Windows puts the interpreter after a "--".
func pythonModule(p Process) string {
	if p.Cmd == nil {
		return ""
	}
	args := p.Cmd.Args
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-m" {
			return args[i+1]
		}
	}
	return ""
}

// Every child the supervisor builds to run from the pipeline's Python environment (a
// `python -m maktaba_*` command line) is one RunsFromVenv names, so the desktop app's
// venv step gates it: a new kind of Python child cannot start before the venv holds
// its module (the 0.3.96 update's plugin runners, gated on the database alone, died on
// "No module named maktaba_plugin_runner" until the crash loop marked them failed).
func TestEveryPythonChildRunsFromTheVenv(t *testing.T) {
	cfg, dir := chromaConfig(t)
	makeChroma(t, dir)
	cfg.Pipeline.Instances = 2
	opts := Options{Config: cfg}
	var all []Process
	main, err := Build(RolePipeline, opts)
	if err != nil {
		t.Fatal(err)
	}
	all = append(all, main)
	for _, build := range []func(Options) ([]Process, error){BuildPipelineInstances, BuildIndexWorker,
		BuildChromaCopy, BuildPluginRunners} {
		ps, err := build(opts)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, ps...)
	}
	modules := map[string]bool{}
	runners := 0
	for _, p := range all {
		m := pythonModule(p)
		if m == "" {
			t.Errorf("%s: no python -m in %q", p.Name(), p.Cmd.Args)
			continue
		}
		modules[m] = true
		if !RunsFromVenv(p) {
			t.Errorf("%s runs python -m %s but does not wait for the venv (RunsFromVenv)", p.Name(), m)
		}
		if p.Role == RolePluginRunner {
			runners++
		}
	}
	if !modules["maktaba_pipeline"] || !modules["maktaba_plugin_runner"] || runners == 0 {
		t.Fatalf("modules %v, %d runners", modules, runners)
	}
	// the children that run no Python do not wait for it
	for _, r := range []Role{RoleAPI, RoleStreaming, RolePostgres, RoleOllama, RolePlugins, RoleLogs, RoleTVSlot} {
		if RunsFromVenv(Process{Role: r}) {
			t.Errorf("%s waits for the venv", r)
		}
	}
}
