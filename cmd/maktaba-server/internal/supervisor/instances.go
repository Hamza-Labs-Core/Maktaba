package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// MaxPipelineInstances bounds [pipeline] instances (the node installer's --instances).
const MaxPipelineInstances = 16

// BuildPipelineInstances builds the extra pipeline workers of [pipeline] instances = N
// (Epic 35 story 35.8 AC7: a converted worker-only node keeps the extra transcription
// workers its installer ran, maktaba-node@1..N): worker i runs only the transcribe stage
// with MAKTABA_STT_SLOT=stt-<i>, its own logs port and no gRPC server; the main worker
// then leaves transcribe to them.
func BuildPipelineInstances(opts Options) ([]Process, error) {
	n := opts.Config.Pipeline.Instances
	if n <= 0 {
		return nil, nil
	}
	if n > MaxPipelineInstances {
		return nil, fmt.Errorf("[pipeline] instances = %d: at most %d", n, MaxPipelineInstances)
	}
	base := ChildEnv(RolePipeline, opts.Config)
	out := make([]Process, 0, n)
	for i := 1; i <= n; i++ {
		logs := "127.0.0.1:" + strconv.Itoa(9110+i)
		env := append(os.Environ(), base...)
		env = setEnv(env, "MAKTABA_STT_SLOT", "stt-"+strconv.Itoa(i))
		env = setEnv(env, "MAKTABA_PIPELINE_STAGES", "transcribe")
		env = setEnv(env, "MAKTABA_PIPELINE_EXCLUDE_STAGES", "")
		env = setEnv(env, "MAKTABA_PIPELINE_GRPC_ADDR", "")
		env = setEnv(env, "MAKTABA_PIPELINE_LOGS_ADDR", logs)
		cmd := exec.Command(pythonFor(opts), "-m", "maktaba_pipeline")
		cmd.Env = env
		wire(cmd)
		out = append(out, Process{Role: RolePipeline, Instance: "stt-" + strconv.Itoa(i), Cmd: cmd,
			Probe: "http://" + logs + "/healthz"})
	}
	return out, nil
}
