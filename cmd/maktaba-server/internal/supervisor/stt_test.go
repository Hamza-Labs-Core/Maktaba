package supervisor

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeMachine makes PlanSTT see an Apple Silicon Mac (or not) with gb of RAM, 8
// logical CPUs and no NVIDIA GPU (fakeGPU gives it one).
func fakeMachine(t *testing.T, apple bool, gb uint64) {
	t.Helper()
	prevA, prevM, prevC := appleSilicon, totalMemory, numCPU
	t.Cleanup(func() { appleSilicon, totalMemory, numCPU = prevA, prevM, prevC })
	appleSilicon = func() bool { return apple }
	totalMemory = func() uint64 { return gb << 30 }
	numCPU = func() int { return 8 }
	fakeGPU(t, "", errors.New("nvidia-smi: not found"))
}

// fakeGPU makes nvidia-smi answer out (or fail with err).
func fakeGPU(t *testing.T, out string, err error) {
	t.Helper()
	prev := nvidiaSMI
	t.Cleanup(func() { nvidiaSMI = prev; forgetGPU() })
	nvidiaSMI = func() ([]byte, error) { return []byte(out), err }
	forgetGPU()
}

// noSTTEnv clears the operator knobs a test's own environment could carry.
func noSTTEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MAKTABA_STT_AUDIO_HOURS", "MAKTABA_STT_GPU_GB", "MAKTABA_STT_GPU_CACHE_GB",
		"MAKTABA_STT_MIN_FREE_PCT", "MAKTABA_STT_BATCH", "MAKTABA_STT_DEVICE", "MAKTABA_STT_COMPUTE_TYPE",
		"MAKTABA_STT_CPU_THREADS", "MAKTABA_PIPELINE_EXCLUDE_STAGES"} {
		t.Setenv(k, "")
	}
}

const rtx2080Super = "NVIDIA GeForce RTX 2080 SUPER, 8192\n"

// num is a knob's value as knobEnv writes it.
func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

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

func TestPlanForGPUTiers(t *testing.T) {
	t.Parallel()
	// this PC's RTX 2080 SUPER (8 GB): one worker
	if p := PlanForGPU(8); p.Workers != 1 || p.Batch != 8 || p.ComputeType != "" {
		t.Errorf("8 GB GPU = %+v", p)
	}
	prev := PlanForGPU(1 << 10)
	for _, gb := range []float64{80, 48, 24, 16, 12, 11, 8, 6, 4, 2} {
		p := PlanForGPU(gb)
		if p.Workers < 1 || p.Workers > prev.Workers || p.Batch < 1 ||
			p.Workers*p.Batch > prev.Workers*prev.Batch {
			t.Errorf("%v GB: %+v is not a step down from %+v", gb, p, prev)
		}
		// a worker's model and CUDA buffers, and 0.14 GB per file of its batch, in two thirds
		model := 2.3
		if p.ComputeType == "int8_float16" {
			model = 1.2
		}
		if need := float64(p.Workers) * (model + 0.14*float64(p.Batch)); gb >= 4 && need > gb*2/3 {
			t.Errorf("%v GB: %+v needs about %.1f GB", gb, p, need)
		}
		if p.GPUGB != 0 || p.GPUCacheGB != 0 || !p.ct2 {
			t.Errorf("%v GB: %+v sets MLX's memory knobs", gb, p)
		}
		prev = p
	}
	if p := PlanForGPU(6); p.ComputeType != "int8_float16" {
		t.Errorf("a 6 GB GPU keeps the weights in int8: %+v", p)
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
	// the CTranslate2 knobs are not MLX's
	c = cfg
	c.Transcription.Device, c.Transcription.ComputeType, c.Transcription.CPUThreads = "cpu", "int8", 4
	if p := PlanSTT(c); p.Device != "" || p.ComputeType != "" || p.CPUThreads != 0 || p.ct2 {
		t.Errorf("Apple Silicon with CTranslate2 knobs = %+v", p)
	}
}

func TestPlanSTTOnTheCPU(t *testing.T) {
	cfg := config.Defaults()
	fakeMachine(t, false, 32)
	host := PlanForRAM(32)
	want := STTPlan{Batch: 4, CPUThreads: 4, AudioHours: host.AudioHours, MinFreePct: host.MinFreePct, ct2: true}
	if p := PlanSTT(cfg); p != want {
		t.Errorf("no GPU = %+v, want %+v (no workers: the main worker transcribes)", p, want)
	}
	numCPU = func() int { return 64 }
	if p := PlanSTT(cfg); p.CPUThreads != 16 {
		t.Errorf("64 CPUs: %d threads, want at most 16", p.CPUThreads)
	}
	numCPU = func() int { return 2 }
	if p := PlanSTT(cfg); p.CPUThreads != 2 {
		t.Errorf("2 CPUs: %d threads, want 2", p.CPUThreads)
	}
	totalMemory = func() uint64 { return 0 }
	if p := PlanSTT(cfg); p.AudioHours != 0 || p.MinFreePct != 0 {
		t.Errorf("unknown RAM leaves the audio budget to the pipeline: %+v", p)
	}

	c := cfg
	c.Pipeline.Instances = 2 // a Linux node's installer ran two workers
	if p := PlanSTT(c); p.Workers != 2 || p.Batch != 4 {
		t.Errorf("a Linux node's instances = %+v", p)
	}
	c = cfg
	c.Transcription.Batch, c.Transcription.CPUThreads, c.Transcription.ComputeType = 2, 6, "INT8_float32"
	c.Transcription.GPUGB, c.Transcription.AudioHours = 9, 5
	if p := PlanSTT(c); p.Batch != 2 || p.CPUThreads != 6 || p.ComputeType != "int8_float32" || p.GPUGB != 0 || p.AudioHours != 5 {
		t.Errorf("[transcription] overrides on the CPU = %+v (gpu_gb is MLX's)", p)
	}
	c = cfg
	c.Transcription.Device, c.Transcription.ComputeType = "auto", "auto"
	if p := PlanSTT(c); p.Device != "" || p.ComputeType != "" {
		t.Errorf("auto is the pipeline's choice: %+v", p)
	}
	c.Transcription.Device = "cuda" // asked for without a GPU: the pipeline says why it cannot
	if p := PlanSTT(c); p.Device != "cuda" || p.Workers != 0 {
		t.Errorf("device = cuda without a GPU = %+v", p)
	}
}

func TestPlanSTTOnAnNvidiaGPU(t *testing.T) {
	cfg := config.Defaults()
	fakeMachine(t, false, 64)
	fakeGPU(t, rtx2080Super, nil)
	host := PlanForRAM(64)
	want := STTPlan{Workers: 1, Batch: 8, AudioHours: host.AudioHours, MinFreePct: host.MinFreePct, ct2: true}
	if p := PlanSTT(cfg); p != want {
		t.Errorf("RTX 2080 SUPER = %+v, want %+v", p, want)
	}
	fakeGPU(t, "NVIDIA GeForce RTX 4090, 24564\nNVIDIA GeForce RTX 3060, 12288\n", nil)
	if p := PlanSTT(cfg); p.Workers != 2 || p.Batch != 16 {
		t.Errorf("RTX 4090 (the first GPU) = %+v", p)
	}
	fakeGPU(t, "NVIDIA GeForce GTX 1650, 4096", nil)
	if p := PlanSTT(cfg); p.Workers != 1 || p.Batch != 4 || p.ComputeType != "int8_float16" {
		t.Errorf("4 GB GPU = %+v", p)
	}

	fakeGPU(t, rtx2080Super, nil)
	c := cfg
	c.Transcription.Device = "CPU" // the owner keeps the GPU for other work
	if p := PlanSTT(c); p.Workers != 0 || p.Batch != 4 || p.Device != "cpu" || p.CPUThreads != 4 {
		t.Errorf("device = cpu on a GPU machine = %+v", p)
	}
	c = cfg
	c.Transcription.Workers, c.Transcription.Batch, c.Transcription.ComputeType = 2, 6, "int8_float16"
	if p := PlanSTT(c); p.Workers != 2 || p.Batch != 6 || p.ComputeType != "int8_float16" {
		t.Errorf("[transcription] overrides on a GPU = %+v", p)
	}
	c = cfg
	c.Pipeline.Instances, c.PipelineInstancesSet, c.Transcription.Workers = 0, true, 2
	if p := PlanSTT(c); p.Workers != 0 || p.Batch != 8 {
		t.Errorf("[pipeline] instances = 0 on a GPU = %+v (the main worker transcribes with the GPU's batch)", p)
	}

	for _, broken := range []struct {
		out string
		err error
	}{{"", errors.New("exit status 9")}, {"No devices were found\n", nil}, {"NVIDIA GeForce RTX 2080 SUPER, [N/A]\n", nil}} {
		fakeGPU(t, broken.out, broken.err)
		if p := PlanSTT(cfg); p.Workers != 0 || p.Batch != cpuBatch {
			t.Errorf("nvidia-smi %q, %v: %+v, want the CPU plan", broken.out, broken.err, p)
		}
	}
}

func TestNvidiaSMIIsAskedOnce(t *testing.T) {
	calls := 0
	fakeGPU(t, "", nil)
	nvidiaSMI = func() ([]byte, error) { calls++; return []byte(rtx2080Super), nil }
	for range 3 {
		if g, ok := NvidiaGPU(); !ok || g.Name != "NVIDIA GeForce RTX 2080 SUPER" || g.MemoryMiB != 8192 || g.MemoryGB() != 8 {
			t.Fatalf("NvidiaGPU = %+v, %v", g, ok)
		}
	}
	if calls != 1 {
		t.Errorf("nvidia-smi ran %d times", calls)
	}
}

func TestParseNvidiaSMI(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		out  string
		want GPU
		ok   bool
	}{
		{rtx2080Super, GPU{"NVIDIA GeForce RTX 2080 SUPER", 8192}, true},
		{"\r\n  Tesla T4 , 15360 \r\n", GPU{"Tesla T4", 15360}, true},
		{"", GPU{}, false},
		{"Failed to initialize NVML: Driver/library version mismatch\n", GPU{}, false},
	} {
		if g, ok := parseNvidiaSMI([]byte(c.out)); g != c.want || ok != c.ok {
			t.Errorf("parse %q = %+v, %v", c.out, g, ok)
		}
	}
	if q := strings.Join(nvidiaSMIQuery, " "); q != "--query-gpu=name,memory.total --format=csv,noheader,nounits" {
		t.Errorf("query = %s", q)
	}
}

func TestPipelineWorkersOnAppleSilicon(t *testing.T) {
	fakeMachine(t, true, 96)
	noSTTEnv(t)
	t.Setenv("MAKTABA_STT_GPU_GB", "30") // the operator's own wins
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "index, transcribe")
	cfg := config.Defaults()
	main := envMap(ChildEnv(RolePipeline, cfg))
	if main["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index,transcribe" {
		t.Fatalf("the main worker must leave transcribe to the workers, keeping the operator's exclusions: %v", main["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
	if _, ok := main["MAKTABA_STT_AUDIO_HOURS"]; ok {
		t.Error("the main worker does not transcribe: no knobs")
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
		for _, k := range []string{"MAKTABA_STT_BATCH", "MAKTABA_STT_DEVICE", "MAKTABA_STT_COMPUTE_TYPE", "MAKTABA_STT_CPU_THREADS"} {
			if v := m[k]; v != "" {
				t.Errorf("worker %d on MLX: %s=%s", i, k, v)
			}
		}
	}
	// workers = -1 on a Mac: the main worker transcribes with the pipeline's own knobs, as before
	cfg.Transcription.Workers = -1
	m := envMap(ChildEnv(RolePipeline, cfg))
	if _, ok := m["MAKTABA_STT_AUDIO_HOURS"]; ok {
		t.Errorf("Mac main worker without workers: %v", m)
	}
	if _, ok := m["MAKTABA_PIPELINE_EXCLUDE_STAGES"]; ok {
		t.Errorf("Mac main worker without workers transcribes: %v", m)
	}
}

func TestPipelineWorkersOnAnNvidiaGPU(t *testing.T) {
	fakeMachine(t, false, 32)
	fakeGPU(t, rtx2080Super, nil)
	noSTTEnv(t)
	t.Setenv("MAKTABA_STT_BATCH", "10") // the operator's own wins
	cfg := config.Defaults()
	cfg.Transcription.ComputeType = "int8_float16"
	main := envMap(ChildEnv(RolePipeline, cfg))
	if main["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "transcribe" {
		t.Errorf("the main worker leaves transcribe to the GPU worker: %q", main["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
	for _, k := range []string{"MAKTABA_STT_AUDIO_HOURS", "MAKTABA_STT_COMPUTE_TYPE", "MAKTABA_STT_CPU_THREADS"} {
		if _, ok := main[k]; ok {
			t.Errorf("the main worker does not transcribe: %s", k)
		}
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("an 8 GB GPU gets one worker: %d, %v", len(procs), err)
	}
	m := envMap(procs[0].Cmd.Env)
	host := PlanForRAM(32)
	if m["MAKTABA_STT_SLOT"] != "stt-1" || m["MAKTABA_PIPELINE_STAGES"] != "transcribe" ||
		m["MAKTABA_STT_COMPUTE_TYPE"] != "int8_float16" || m["MAKTABA_STT_BATCH"] != "10" ||
		m["MAKTABA_STT_AUDIO_HOURS"] != num(host.AudioHours) || m["MAKTABA_STT_MIN_FREE_PCT"] != num(host.MinFreePct) {
		t.Errorf("GPU worker env: %v", m)
	}
	for _, k := range []string{"MAKTABA_STT_GPU_GB", "MAKTABA_STT_GPU_CACHE_GB", "MAKTABA_STT_DEVICE", "MAKTABA_STT_CPU_THREADS"} {
		if v := m[k]; v != "" {
			t.Errorf("GPU worker: %s=%s (MLX's, or left to the pipeline)", k, v)
		}
	}
}

func TestTheMainWorkerTranscribesOnTheCPU(t *testing.T) {
	fakeMachine(t, false, 16)
	noSTTEnv(t)
	cfg := config.Defaults()
	cfg.Transcription.Device = "cpu"
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 0 {
		t.Fatalf("no GPU: %d workers, %v", len(procs), err)
	}
	m := envMap(ChildEnv(RolePipeline, cfg))
	host := PlanForRAM(16)
	if m["MAKTABA_STT_BATCH"] != "4" || m["MAKTABA_STT_CPU_THREADS"] != "4" || m["MAKTABA_STT_DEVICE"] != "cpu" ||
		m["MAKTABA_STT_AUDIO_HOURS"] != num(host.AudioHours) {
		t.Errorf("the main worker's knobs: %v", m)
	}
	if v, ok := m["MAKTABA_PIPELINE_EXCLUDE_STAGES"]; ok {
		t.Errorf("the main worker transcribes: exclude = %q", v)
	}
	t.Setenv("MAKTABA_STT_CPU_THREADS", "12")
	if m := envMap(ChildEnv(RolePipeline, cfg)); m["MAKTABA_STT_CPU_THREADS"] != "" {
		t.Errorf("the operator's own MAKTABA_STT_CPU_THREADS must be kept: %q", m["MAKTABA_STT_CPU_THREADS"])
	}
	if api := envMap(ChildEnv(RoleAPI, cfg)); api["MAKTABA_STT_BATCH"] != "" {
		t.Error("the transcription knobs are the pipeline's only")
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
