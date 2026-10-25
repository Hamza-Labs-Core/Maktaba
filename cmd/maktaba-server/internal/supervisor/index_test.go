package supervisor

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// sttExclude is the main worker's excluded stages without IndexStages (the index
// worker's): what the transcription plan decided.
func sttExclude(env map[string]string) string {
	var out []string
	for _, s := range strings.Split(env["MAKTABA_PIPELINE_EXCLUDE_STAGES"], ",") {
		if s != "" && !hasStage(IndexStages, s) {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}

func indexConfig(t *testing.T) config.Config {
	t.Helper()
	noSTTEnv(t)
	t.Setenv("MAKTABA_ROLE", "")
	t.Setenv("MAKTABA_PIPELINE_SHARE", "")
	t.Setenv("MAKTABA_PIPELINE_SEMANTIC", "")
	cfg := config.Defaults()
	cfg.Server.DataDir = t.TempDir()
	return cfg
}

// TestTheIndexWorker: on a home the search index (index, chapter_infer and the
// semantic search) runs in a worker of its own that never touches the share, and the
// main worker opens no Chroma: one process per Chroma directory, and the share's
// stalls that get the main worker killed cannot cut an index write in half.
func TestTheIndexWorker(t *testing.T) {
	cfg := indexConfig(t)
	procs, err := BuildIndexWorker(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("index worker = %d, %v", len(procs), err)
	}
	p := procs[0]
	if p.Role != RolePipeline || p.Instance != IndexInstance || p.Name() != "pipeline/index" {
		t.Errorf("process = %s", p.Name())
	}
	if p.Probe != "http://127.0.0.1:9104/healthz" || p.ProbeTimeout != PythonProbeTimeout {
		t.Errorf("probe = %s, %s", p.Probe, p.ProbeTimeout)
	}
	m := envMap(p.Cmd.Env)
	for k, want := range map[string]string{
		"MAKTABA_PIPELINE_STAGES":         IndexStages,
		"MAKTABA_PIPELINE_EXCLUDE_STAGES": "",
		"MAKTABA_PIPELINE_GRPC_ADDR":      "127.0.0.1:9093",
		"MAKTABA_PIPELINE_LOGS_ADDR":      "127.0.0.1:9104",
		"MAKTABA_PIPELINE_SHARE":          "off",
		"MAKTABA_PIPELINE_SEMANTIC":       "",
	} {
		if m[k] != want {
			t.Errorf("index worker %s = %q, want %q", k, m[k], want)
		}
	}
	if m["MAKTABA_CHROMA_DIR"] == "" {
		t.Error("the index worker must know the Chroma directory")
	}

	main := envMap(mainProcess(t, cfg).Cmd.Env)
	if ex := main["MAKTABA_PIPELINE_EXCLUDE_STAGES"]; !hasStage(ex, "index") || !hasStage(ex, "chapter_infer") {
		t.Errorf("the main worker must leave the index stages to the index worker: %q", ex)
	}
	if main["MAKTABA_PIPELINE_SEMANTIC"] != "off" || main["MAKTABA_PIPELINE_SHARE"] != "" {
		t.Errorf("the main worker: semantic %q, share %q", main["MAKTABA_PIPELINE_SEMANTIC"], main["MAKTABA_PIPELINE_SHARE"])
	}
	if main["MAKTABA_PIPELINE_GRPC_ADDR"] != "127.0.0.1:9090" || main["MAKTABA_CHROMA_DIR"] != m["MAKTABA_CHROMA_DIR"] {
		t.Errorf("the main worker keeps its gRPC port: %q", main["MAKTABA_PIPELINE_GRPC_ADDR"])
	}

	api := envMap(childEnvOK(RoleAPI, cfg))
	if api["MAKTABA_PIPELINE_SEARCH_ADDR"] != "127.0.0.1:9093" || api["MAKTABA_PIPELINE_ADDR"] != "127.0.0.1:9090" {
		t.Errorf("the api asks the index worker for the semantic search: %q / %q",
			api["MAKTABA_PIPELINE_SEARCH_ADDR"], api["MAKTABA_PIPELINE_ADDR"])
	}
	if !strings.Contains(api["MAKTABA_LOG_PEERS"], "pipeline-index=http://127.0.0.1:9104/logs/recent") {
		t.Errorf("the diagnostics carry the index worker's logs: %q", api["MAKTABA_LOG_PEERS"])
	}

	// [server] moves its ports
	cfg.Server.PipelineSearchAddr = "127.0.0.1:19093"
	cfg.Server.PipelineIndexLogsAddr = "127.0.0.1:19104"
	procs, _ = BuildIndexWorker(Options{Config: cfg})
	if m := envMap(procs[0].Cmd.Env); m["MAKTABA_PIPELINE_GRPC_ADDR"] != "127.0.0.1:19093" ||
		procs[0].Probe != "http://127.0.0.1:19104/healthz" {
		t.Errorf("[server] ports: %q, %s", m["MAKTABA_PIPELINE_GRPC_ADDR"], procs[0].Probe)
	}
	if v := envMap(childEnvOK(RoleAPI, cfg))["MAKTABA_PIPELINE_SEARCH_ADDR"]; v != "127.0.0.1:19093" {
		t.Errorf("api search address = %q", v)
	}
}

// TestTheIndexWorkerKeepsTheTranscriptionPlan: beside the transcription workers the
// main worker leaves both transcribe and the index stages, keeping the operator's own
// exclusions.
func TestTheIndexWorkerKeepsTheTranscriptionPlan(t *testing.T) {
	cfg := indexConfig(t)
	cfg.Pipeline.Instances = 2
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "tag")
	ex := envMap(mainProcess(t, cfg).Cmd.Env)["MAKTABA_PIPELINE_EXCLUDE_STAGES"]
	for _, s := range []string{"tag", "transcribe", "index", "chapter_infer"} {
		if !hasStage(ex, s) {
			t.Errorf("main worker exclude %q lacks %s", ex, s)
		}
	}
	procs, err := BuildIndexWorker(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("index worker = %d, %v", len(procs), err)
	}
	if v := envMap(procs[0].Cmd.Env)["MAKTABA_PIPELINE_EXCLUDE_STAGES"]; v != "tag" {
		t.Errorf("the operator's exclusions reach the index worker as they are: %q", v)
	}
}

// TestNoIndexWorker: [pipeline] index_worker = "off", an operator who places the
// index stage (excluded, or the main worker's stages listed by hand) and a node (the
// index is home-only) run as before: the main worker keeps the index and answers the
// semantic search, and the api asks it.
func TestNoIndexWorker(t *testing.T) {
	joined := config.NodeConfig{NodeID: "6f1c9a2e-0000-4000-8000-000000000001", PrivateKey: "AQID",
		DatabaseURL: "postgres://node_6f1c9a2e@home:5432/maktaba", Home: config.HomeRef{URL: "http://home:8080"}}
	for _, c := range []struct {
		name             string
		off              bool
		exclude, stages  string
		node             bool
		wantMainExcluded string
	}{
		{name: "index_worker off", off: true},
		{name: "operator excludes index", exclude: "index", wantMainExcluded: "index"},
		{name: "operator lists the stages", stages: "probe,index"},
		{name: "a node", node: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := indexConfig(t)
			if c.off {
				cfg.Pipeline.IndexWorker = "off"
			}
			t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", c.exclude)
			t.Setenv("MAKTABA_PIPELINE_STAGES", c.stages)
			if c.node {
				prev := nodeConfig
				t.Cleanup(func() { nodeConfig = prev })
				nodeConfig = func() config.NodeConfig { return joined }
			}
			procs, err := BuildIndexWorker(Options{Config: cfg})
			if err != nil || len(procs) != 0 {
				t.Fatalf("index worker = %d, %v", len(procs), err)
			}
			main := envMap(mainProcess(t, cfg).Cmd.Env)
			// the transcription plan may add transcribe: only the index stages count here
			var excluded []string
			for _, s := range strings.Split(main["MAKTABA_PIPELINE_EXCLUDE_STAGES"], ",") {
				if hasStage(IndexStages, s) {
					excluded = append(excluded, s)
				}
			}
			if v := strings.Join(excluded, ","); v != c.wantMainExcluded {
				t.Errorf("main worker excludes %q of the index stages, want %q", v, c.wantMainExcluded)
			}
			if v := main["MAKTABA_PIPELINE_SEMANTIC"]; v != "" {
				t.Errorf("the main worker answers the semantic search: %q", v)
			}
			if v := envMap(childEnvOK(RoleAPI, cfg))["MAKTABA_PIPELINE_SEARCH_ADDR"]; v != "" {
				t.Errorf("api search address = %q", v)
			}
		})
	}
}

// TestTheIndexWorkersPortsAreItsOwn: the index worker's two ports are none of the
// other processes' defaults (the roles, the transcription workers, the TV slots).
func TestTheIndexWorkersPortsAreItsOwn(t *testing.T) {
	taken := map[string]string{
		"127.0.0.1:9050": "streaming grpc", "127.0.0.1:9090": "pipeline grpc", "127.0.0.1:9100": "api admin",
		"127.0.0.1:9101": "streaming admin", "127.0.0.1:9102": "pipeline logs", "127.0.0.1:9103": "supervisor",
		"127.0.0.1:9091": "streaming metrics",
	}
	for i := 1; i <= MaxPipelineInstances; i++ {
		taken["127.0.0.1:"+strconv.Itoa(9110+i)] = "stt-" + strconv.Itoa(i)
	}
	for _, s := range SlotNames {
		taken[SlotAdminAddr(s)] = "slot " + s
	}
	for _, addr := range []string{pipelineSearchAddr, pipelineIndexLogsAddr} {
		if who, ok := taken[addr]; ok {
			t.Errorf("%s is %s's", addr, who)
		}
	}
}
