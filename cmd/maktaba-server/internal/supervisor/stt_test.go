package supervisor

import (
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeMachine makes PlanSTT see an Apple Silicon Mac (or not) with gb of RAM.
func fakeMachine(t *testing.T, apple bool, gb uint64) {
	t.Helper()
	prevA, prevM := appleSilicon, totalMemory
	t.Cleanup(func() { appleSilicon, totalMemory = prevA, prevM })
	appleSilicon = func() bool { return apple }
	totalMemory = func() uint64 { return gb << 30 }
}

func TestPlanForRAMTopTierIsTheHomeMac(t *testing.T) {
	t.Parallel()
	// tools/dev/stack.sh on the 96 GB Mac: 2 workers, 16 h, 20 GB, 2 GB, 25%
	if p := PlanForRAM(96); p != (STTPlan{Workers: 2, AudioHours: 16, GPUGB: 20, GPUCacheGB: 2, MinFreePct: 25}) {
		t.Fatalf("96 GB plan = %+v", p)
	}
	prev := PlanForRAM(1 << 10)
	for _, gb := range []float64{128, 96, 64, 48, 36, 32, 24, 18, 16, 8} {
		p := PlanForRAM(gb)
		if p.Workers < 1 || p.Workers > prev.Workers || p.GPUGB > prev.GPUGB || p.AudioHours > prev.AudioHours {
			t.Errorf("%v GB: %+v is not a step down from %+v", gb, p, prev)
		}
		if float64(p.Workers)*(p.GPUGB+p.GPUCacheGB) > gb/2 {
			t.Errorf("%v GB: %d workers × %v GB would take over half the memory", gb, p.Workers, p.GPUGB+p.GPUCacheGB)
		}
		prev = p
	}
}

func TestPlanSTT(t *testing.T) {
	cfg := config.Defaults()
	fakeMachine(t, true, 96)
	if p := PlanSTT(cfg); p.Workers != 2 || p.GPUGB != 20 {
		t.Errorf("Apple Silicon 96 GB = %+v", p)
	}
	fakeMachine(t, true, 16)
	if p := PlanSTT(cfg); p.Workers != 1 {
		t.Errorf("Apple Silicon 16 GB = %+v, want one worker", p)
	}

	c := cfg
	c.Transcription.Workers, c.Transcription.GPUGB = 3, 9
	if p := PlanSTT(c); p.Workers != 3 || p.GPUGB != 9 || p.AudioHours != PlanForRAM(16).AudioHours {
		t.Errorf("[transcription] overrides = %+v", p)
	}
	c = cfg
	c.Transcription.Workers = -1
	if p := PlanSTT(c); p.Workers != 0 {
		t.Errorf("workers = -1: %+v", p)
	}
	// a node's [pipeline] instances wins, 0 included
	c = cfg
	c.Pipeline.Instances, c.PipelineInstancesSet, c.Transcription.Workers = 0, true, 2
	if p := PlanSTT(c); p.Workers != 0 {
		t.Errorf("[pipeline] instances = 0 set: %+v", p)
	}

	fakeMachine(t, false, 256)
	if p := PlanSTT(cfg); p != (STTPlan{}) {
		t.Errorf("not Apple Silicon = %+v, want nothing (one worker, the pipeline's knobs)", p)
	}
	c = cfg
	c.Pipeline.Instances = 2
	if p := PlanSTT(c); p.Workers != 2 || len(p.knobEnv()) != 0 {
		t.Errorf("a Linux node's instances = %+v (knobs %v)", p, p.knobEnv())
	}
}

func TestPipelineWorkersOnAppleSilicon(t *testing.T) {
	fakeMachine(t, true, 96)
	for _, k := range []string{"MAKTABA_STT_AUDIO_HOURS", "MAKTABA_STT_GPU_GB", "MAKTABA_STT_GPU_CACHE_GB", "MAKTABA_STT_MIN_FREE_PCT"} {
		t.Setenv(k, "")
	}
	t.Setenv("MAKTABA_STT_GPU_GB", "30") // the operator's own wins
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "index, transcribe")
	cfg := config.Defaults()
	if m := envMap(ChildEnv(RolePipeline, cfg)); m["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index,transcribe" {
		t.Fatalf("the main worker must leave transcribe to the workers, keeping the operator's exclusions: %v", m["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 2 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	for i, p := range procs {
		m := envMap(p.Cmd.Env)
		slot := "stt-" + string(rune('1'+i))
		if p.Instance != slot || m["MAKTABA_STT_SLOT"] != slot || m["MAKTABA_PIPELINE_STAGES"] != "transcribe" ||
			m["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "" || m["MAKTABA_PIPELINE_GRPC_ADDR"] != "" {
			t.Errorf("worker %d: %s %v", i, p.Instance, m)
		}
		if m["MAKTABA_STT_AUDIO_HOURS"] != "16" || m["MAKTABA_STT_GPU_CACHE_GB"] != "2" || m["MAKTABA_STT_MIN_FREE_PCT"] != "25" ||
			m["MAKTABA_STT_GPU_GB"] != "30" {
			t.Errorf("worker %d knobs: %v", i, m)
		}
	}
}

func TestTranscriptionEnv(t *testing.T) {
	for _, k := range []string{"MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL", "MAKTABA_PATH_MAP", "MAKTABA_WORKER_PATH_MAP"} {
		t.Setenv(k, "")
	}
	cfg := config.Defaults()
	cfg.Transcription.Model = "large-v3" // an old setup's short name: the pipeline's default
	m := envMap(ChildEnv(RolePipeline, cfg))
	if _, ok := m["MAKTABA_STT_MODEL"]; ok {
		t.Errorf("an old short model name must not reach the pipeline: %q", m["MAKTABA_STT_MODEL"])
	}
	for k := range m {
		if strings.HasPrefix(k, "MAKTABA_TRANSCRIPTION_") {
			t.Errorf("%s is read by nothing", k)
		}
	}
	cfg.Transcription.Model = "mlx-community/whisper-large-v3-turbo"
	cfg.Transcription.Refine = "off"
	cfg.Transcription.RefineModel = "/Volumes/nas/models/cohere"
	cfg.Media.PathMap = "/Volumes/Originals/=/Users/me/nas/"
	m = envMap(ChildEnv(RolePipeline, cfg))
	if m["MAKTABA_STT_MODEL"] != cfg.Transcription.Model || m["MAKTABA_STT_REFINE"] != "0" ||
		m["MAKTABA_STT_REFINE_MODEL"] != cfg.Transcription.RefineModel || m["MAKTABA_WORKER_PATH_MAP"] != cfg.Media.PathMap {
		t.Errorf("pipeline env = %v", m)
	}
	if api := envMap(ChildEnv(RoleAPI, cfg)); api["MAKTABA_PATH_MAP"] != cfg.Media.PathMap || api["MAKTABA_STT_MODEL"] != "" {
		t.Errorf("api env: path map %q, stt model %q", api["MAKTABA_PATH_MAP"], api["MAKTABA_STT_MODEL"])
	}
	if _, ok := m["MAKTABA_PATH_MAP"]; ok {
		t.Error("the pipeline reads MAKTABA_PATH_MAP as Plex's map: it gets MAKTABA_WORKER_PATH_MAP only")
	}
	t.Setenv("MAKTABA_STT_MODEL", "own/model")
	t.Setenv("MAKTABA_PATH_MAP", "/own=/x")
	m = envMap(ChildEnv(RolePipeline, cfg))
	if _, ok := m["MAKTABA_STT_MODEL"]; ok {
		t.Error("the operator's own MAKTABA_STT_MODEL must be kept")
	}
	if _, ok := m["MAKTABA_PATH_MAP"]; ok {
		t.Error("the operator's own MAKTABA_PATH_MAP must be kept")
	}
}
