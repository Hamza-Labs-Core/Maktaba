package supervisor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// IndexStages are the stages that open the search index (Chroma): index writes the
// vectors, chapter_infer reads them back. Chroma wants one process per directory, so
// they run in one worker, the one that also answers the semantic search.
const IndexStages = "index,chapter_infer"

// IndexInstance is the index worker's instance name (pipeline/index).
const IndexInstance = "index"

const (
	// IndexDrain is how long the index worker's jobs in flight may take once it is
	// asked to stop (MAKTABA_PIPELINE_DRAIN_SEC): no new search index write starts
	// then, and the one running finishes (search/gate.py), so most stops take seconds.
	IndexDrain = 30 * time.Second
	// IndexStopGrace is its stop grace: the drain, the Chroma close and the exit after
	// it. Within the launcher's 60 s, which also stops postgres after the roles.
	IndexStopGrace = 45 * time.Second
)

var (
	// pipelineSearchAddr is the index worker's gRPC port (the semantic search).
	pipelineSearchAddr = ports.IndexSearch.Addr()
	// pipelineIndexLogsAddr is the index worker's HTTP port (/logs/recent, /healthz).
	pipelineIndexLogsAddr = ports.IndexLogs.Addr()
)

func pipelineSearch(cfg config.Config) string {
	return orDefault(cfg.Server.PipelineSearchAddr, pipelineSearchAddr)
}
func pipelineIndexLogs(cfg config.Config) string {
	return orDefault(cfg.Server.PipelineIndexLogsAddr, pipelineIndexLogsAddr)
}

// IndexWorkerOn reports whether the search index runs in a pipeline worker of its own
// (BuildIndexWorker): [pipeline] index_worker is not off, this machine is a home (the
// index stage is home-only: a node's pipeline never claims it), and the operator's
// own environment does not place the index stage itself (operatorKeepsIndexOff). The main worker then leaves IndexStages to it and opens no
// Chroma, so the share's stalls that get it killed can no longer cut a write in half
// (three times on the home Mac, 2026-10-23..25: "Error constructing hnsw segment").
func IndexWorkerOn(cfg config.Config, n config.NodeConfig) bool {
	return cfg.Pipeline.IndexWorkerOn() && !NodeMode(cfg, n) && !operatorKeepsIndexOff()
}

// operatorKeepsIndexOff reports whether the operator's own environment places the
// index stage itself: MAKTABA_PIPELINE_EXCLUDE_STAGES names it, or
// MAKTABA_PIPELINE_STAGES lists the main worker's stages by hand. No index worker
// then, and the main worker keeps the operator's stages as they are.
func operatorKeepsIndexOff() bool {
	return hasStage(os.Getenv("MAKTABA_PIPELINE_EXCLUDE_STAGES"), "index") ||
		strings.TrimSpace(os.Getenv("MAKTABA_PIPELINE_STAGES")) != ""
}

// BuildIndexWorker builds the index worker beside the main worker when IndexWorkerOn:
// it runs only IndexStages, serves the semantic search on its own gRPC port (the api
// gets it as MAKTABA_PIPELINE_SEARCH_ADDR), has its own logs and liveness port, and
// runs with MAKTABA_PIPELINE_SHARE=off: none of the storage checks or share probes
// every other worker makes, so nothing in it waits on the share. Its drain and stop
// grace (IndexDrain, IndexStopGrace) are longer than the others': it is never killed
// in the middle of a write. The operator's own stage exclusions reach it as they are.
func BuildIndexWorker(opts Options) ([]Process, error) {
	n, err := NodeOf(opts.Config)
	if err != nil {
		return nil, err
	}
	if !IndexWorkerOn(opts.Config, n) {
		return nil, nil
	}
	base, err := ChildEnv(RolePipeline, opts.Config)
	if err != nil {
		return nil, err
	}
	logs := pipelineIndexLogs(opts.Config)
	env := append(os.Environ(), base...)
	env = setEnv(env, "MAKTABA_PIPELINE_STAGES", IndexStages)
	env = setEnv(env, "MAKTABA_PIPELINE_GRPC_ADDR", pipelineSearch(opts.Config))
	env = setEnv(env, "MAKTABA_PIPELINE_LOGS_ADDR", logs)
	env = setEnv(env, "MAKTABA_PIPELINE_SHARE", "off")
	env = setEnv(env, "MAKTABA_PIPELINE_DRAIN_SEC", strconv.Itoa(int(IndexDrain.Seconds())))
	cmd := exec.Command(pythonFor(opts), "-m", "maktaba_pipeline")
	cmd.Env = env
	wire(cmd)
	return []Process{{Role: RolePipeline, Instance: IndexInstance, Cmd: cmd,
		Probe: "http://" + LocalAddr(logs, ports.IndexLogs.PortString()) + "/healthz", ProbeTimeout: PythonProbeTimeout,
		StopGrace: IndexStopGrace,
		Binds:     []ports.Bind{{Addr: pipelineSearch(opts.Config), What: "search port"}, {Addr: logs, What: "logs port"}}}}, nil
}

// indexMainEnv is the main worker's side of the index worker: it leaves IndexStages
// to it (added to the stages env already excludes) and answers no semantic search
// (MAKTABA_PIPELINE_SEMANTIC=off: it never opens Chroma).
func indexMainEnv(cfg config.Config, env []string) ([]string, error) {
	n, err := NodeOf(cfg)
	if err != nil {
		return nil, err
	}
	if !IndexWorkerOn(cfg, n) {
		return env, nil
	}
	exclude := envValue(env, "MAKTABA_PIPELINE_EXCLUDE_STAGES")
	for _, s := range strings.Split(IndexStages, ",") {
		exclude = withStage(exclude, s)
	}
	env = setEnv(env, "MAKTABA_PIPELINE_EXCLUDE_STAGES", exclude)
	return setEnv(env, "MAKTABA_PIPELINE_SEMANTIC", "off"), nil
}

// envValue is k's value in env (the last one wins, as exec does), or "".
func envValue(env []string, k string) string {
	v := ""
	for _, e := range env {
		if rest, ok := strings.CutPrefix(e, k+"="); ok {
			v = rest
		}
	}
	return v
}
