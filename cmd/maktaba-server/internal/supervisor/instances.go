package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// MaxPipelineInstances bounds [pipeline] instances (the node installer's --instances).
const MaxPipelineInstances = 16

// BuildPipelineInstances builds the transcription workers beside the main worker
// (PlanSTT): [pipeline] instances = N on a converted worker-only node (Epic 35 story
// 35.8 AC7: the workers its installer ran, maktaba-node@1..N), or on Apple Silicon a
// number sized from the RAM (Epic 36 story 36.4, as the home Mac runs them). Worker i
// runs only the transcribe stage with MAKTABA_STT_SLOT=stt-<i> (its stt.* metrics keep
// their series across restarts), the plan's memory knobs, its own logs port and no
// gRPC server; the main worker then leaves transcribe to them.
func BuildPipelineInstances(opts Options) ([]Process, error) {
	plan := PlanSTT(opts.Config)
	n := plan.Workers
	if n <= 0 {
		return nil, nil
	}
	if n > MaxPipelineInstances {
		return nil, fmt.Errorf("%d transcription workers ([pipeline] instances / [transcription] workers): at most %d", n, MaxPipelineInstances)
	}
	base := ChildEnv(RolePipeline, opts.Config)
	out := make([]Process, 0, n)
	for i := 1; i <= n; i++ {
		logs := "127.0.0.1:" + strconv.Itoa(9110+i)
		env := append(os.Environ(), base...)
		env = append(env, plan.knobEnv()...)
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
