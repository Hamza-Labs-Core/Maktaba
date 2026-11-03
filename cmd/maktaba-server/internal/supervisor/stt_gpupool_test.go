package supervisor

import (
	"errors"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// The Windows node (RTX 2080 SUPER, 8 GB): the gpu pool is per worker, and the
// cluster's size was set for the home's GPU (2 on the live Mac). The worker that
// transcribes there takes the batch sized from its own GPU (MAKTABA_POOL_GPU, which the
// pipeline's claim loop reads): 8 files at once, the batch that ran at 72x. The second
// listen is a stage of its own that the same worker runs, taking turns with Whisper on
// the 8 GB card; on the CPU and on a Mac no size of its own (the cluster's pool stands).
func TestPlanSTTSizesTheGPUPoolPerNode(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 128)
	fakeGPU(t, "NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 581.57\r\n", nil)
	cfg := noRefine()
	procs := buildPipelineOK(t, cfg)
	if len(procs) != 1 {
		t.Fatalf("an 8 GB GPU gets one worker: %d", len(procs))
	}
	if m := envMap(procs[0].Cmd.Env); m["MAKTABA_POOL_GPU"] != "8" || m["MAKTABA_STT_BATCH"] != "8" {
		t.Errorf("the GPU worker's own gpu pool and batch: %v", m)
	}
	if m := envMap(mainProcess(t, cfg).Cmd.Env); m["MAKTABA_POOL_GPU"] != "" {
		t.Errorf("the main worker leaves transcribe to the GPU worker: MAKTABA_POOL_GPU=%s", m["MAKTABA_POOL_GPU"])
	}

	on := config.Defaults() // the second listen on: Whisper and it take turns on the card
	if p := PlanSTT(on); p.Workers != 1 || !p.gpu || p.Batch != 8 {
		t.Errorf("an 8 GB GPU with the second listen on = %+v", p)
	}
	if m := envMap(buildPipelineOK(t, on)[0].Cmd.Env); m["MAKTABA_POOL_GPU"] != "8" || m["MAKTABA_POOL_LISTEN"] != "8" {
		t.Errorf("the GPU worker's own pools: a batch of each stage: %v", m)
	}

	c := cfg
	c.Transcription.Batch = 6 // the operator's batch is the pool too
	if m := envMap(buildPipelineOK(t, c)[0].Cmd.Env); m["MAKTABA_POOL_GPU"] != "6" {
		t.Errorf("[transcription] batch = 6: MAKTABA_POOL_GPU=%s", m["MAKTABA_POOL_GPU"])
	}
	t.Setenv("MAKTABA_POOL_GPU", "3") // the operator's own environment stands
	if m := envMap(buildPipelineOK(t, cfg)[0].Cmd.Env); m["MAKTABA_POOL_GPU"] != "3" {
		t.Errorf("the operator's MAKTABA_POOL_GPU: %s", m["MAKTABA_POOL_GPU"])
	}
	t.Setenv("MAKTABA_POOL_GPU", "")

	fakeGPU(t, "", errors.New("no nvidia-smi"))
	if m := envMap(mainProcess(t, cfg).Cmd.Env); m["MAKTABA_POOL_GPU"] != "" {
		t.Errorf("the CPU plan has no gpu pool of its own: %s", m["MAKTABA_POOL_GPU"])
	}
	fakeMachine(t, true, 96)
	for _, p := range append(buildPipelineOK(t, cfg), mainProcess(t, cfg)) {
		if m := envMap(p.Cmd.Env); m["MAKTABA_POOL_GPU"] != "" {
			t.Errorf("a Mac keeps the cluster's gpu pool: %s", m["MAKTABA_POOL_GPU"])
		}
	}
}

// The pool sizes follow the GPU's memory: each tier's batch fits its card (the batch the
// pool lets in at once never exceeds what the plan sized for that GPU).
func TestPlanSTTGPUPoolFollowsTheCard(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 64)
	for _, c := range []struct {
		smi  string
		pool string
	}{
		{"NVIDIA GeForce GTX 1650, 4096, 7.5, 581.57", "4"},
		{"NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 581.57", "8"},
		{"NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44", "16"},
		{"NVIDIA GeForce RTX 4090, 24564, 8.9, 591.44", "16"},
	} {
		fakeGPU(t, c.smi, nil)
		procs := buildPipelineOK(t, noRefine())
		if len(procs) == 0 {
			t.Fatalf("%s: no worker", c.smi)
		}
		if m := envMap(procs[0].Cmd.Env); m["MAKTABA_POOL_GPU"] != c.pool {
			t.Errorf("%s: MAKTABA_POOL_GPU=%s, want %s", c.smi, m["MAKTABA_POOL_GPU"], c.pool)
		}
	}
}

func buildPipelineOK(t *testing.T, cfg config.Config) []Process {
	t.Helper()
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return procs
}
