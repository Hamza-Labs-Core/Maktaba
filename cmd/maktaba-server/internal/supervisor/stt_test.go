package supervisor

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// fakeMachine makes PlanSTT see an Apple Silicon Mac (or not: an x86_64 Windows or
// Linux machine) with gb of RAM, 8 logical CPUs, no NVIDIA GPU (fakeGPU gives it
// one) and a pipeline Python with the stt extra. The second listen fits on a GPU of
// 9.5 GiB or more, as on Windows, on every OS (fakeRefineMin changes it).
func fakeMachine(t *testing.T, apple bool, gb uint64) {
	t.Helper()
	prevA, prevM, prevC, prevW, prevS, prevL := appleSilicon, totalMemory, numCPU, cudaWhisper, sttInstalled, secondListenModel
	prevT := venvTorch
	t.Cleanup(func() {
		appleSilicon, totalMemory, numCPU, cudaWhisper, sttInstalled, secondListenModel = prevA, prevM, prevC, prevW, prevS, prevL
		venvTorch = prevT
	})
	appleSilicon = func() bool { return apple }
	totalMemory = func() uint64 { return gb << 30 }
	numCPU = func() int { return 8 }
	cudaWhisper = func() bool { return !apple }
	sttInstalled = func(string) bool { return true }
	venvTorch = func(string) string { return torchCUDA }
	secondListenModel = func(config.Config) bool { return true } // a token, or a copy here
	fakeGPU(t, "", errors.New("nvidia-smi: not found"))
	fakeRefineMin(t, 9.5)
}

// fakeRefineMin sets the smallest GPU the second listen fits on (refineMinGB).
func fakeRefineMin(t *testing.T, gb float64) {
	t.Helper()
	prev := refineMinGB
	t.Cleanup(func() { refineMinGB = prev })
	refineMinGB = gb
}

// fakeGPU makes nvidia-smi answer out (or fail with err).
func fakeGPU(t *testing.T, out string, err error) {
	t.Helper()
	prev := nvidiaSMI
	t.Cleanup(func() { nvidiaSMI = prev; forgetGPU() })
	nvidiaSMI = func() ([]byte, error) { return []byte(out), err }
	forgetGPU()
}

// noSTTEnv clears the operator settings a test's own environment could carry.
func noSTTEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"MAKTABA_STT_AUDIO_HOURS", "MAKTABA_STT_GPU_GB", "MAKTABA_STT_GPU_CACHE_GB",
		"MAKTABA_STT_MIN_FREE_PCT", "MAKTABA_STT_BATCH", "MAKTABA_STT_DEVICE", "MAKTABA_STT_COMPUTE_TYPE",
		"MAKTABA_STT_CPU_THREADS", "MAKTABA_STT_REFINE", "MAKTABA_PIPELINE_EXCLUDE_STAGES", "MAKTABA_PIPELINE_STAGES"} {
		t.Setenv(k, "")
	}
}

// noRefine is a config whose second listen is off: Windows and Linux then plan
// their transcription workers (with it on they wait for a machine that runs it).
func noRefine() config.Config {
	cfg := config.Defaults()
	cfg.Transcription.Refine = "off"
	return cfg
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

// gpuNeed is what n workers of plan p take on the GPU (the measured sizes).
func gpuNeed(p STTPlan, n int) float64 {
	return float64(n) * (ct2ModelGB[p.ComputeType] + ct2FileGB*float64(p.Batch))
}

func TestPlanForGPUTiers(t *testing.T) {
	t.Parallel()
	// this PC's RTX 2080 SUPER (8 GB): one worker
	if p, ok := PlanForGPU(8); !ok || p.Workers != 1 || p.Batch != 8 || p.ComputeType != "" {
		t.Errorf("8 GB GPU = %+v, %v", p, ok)
	}
	prev, _ := PlanForGPU(1 << 10)
	for _, gb := range []float64{80, 48, 24, 16, 12, 11, 8, 6, 4} {
		p, ok := PlanForGPU(gb)
		if !ok || p.Workers < 1 || p.Workers > prev.Workers || p.Batch < 1 ||
			p.Workers*p.Batch > prev.Workers*prev.Batch {
			t.Errorf("%v GB: %+v is not a step down from %+v", gb, p, prev)
		}
		// a worker's model and CUDA buffers, and 0.14 GB per file of its batch, in two thirds
		if need := gpuNeed(p, p.Workers); need > gb*gpuShare {
			t.Errorf("%v GB: %+v needs about %.1f GB", gb, p, need)
		}
		if p.GPUGB != 0 || p.GPUCacheGB != 0 || !p.ct2 {
			t.Errorf("%v GB: %+v sets MLX's memory knobs", gb, p)
		}
		prev = p
	}
	if p, _ := PlanForGPU(6); p.ComputeType != "int8_float16" {
		t.Errorf("a 6 GB GPU keeps the weights in int8: %+v", p)
	}
	// a 2 or 3 GB card ran out of memory loading the model: the CPU's
	for _, gb := range []float64{3.9, 3, 2, 0.5} {
		if p, ok := PlanForGPU(gb); ok {
			t.Errorf("%v GB GPU = %+v: under %v GB Whisper goes on the CPU", gb, p, float64(gpuMinGB))
		}
	}
}

// The pipeline's check before the second listen's model loads (stt/cohere_cuda.py):
// free GPU memory >= its weights, what loading takes besides, a batch of four
// stretches, and Whisper's room (0.14 GiB per file of its batch).
const (
	cohereWeightsGB = 3.85
	stretchGB       = 0.09
	whisperFileGB   = 0.14
)

func secondListenNeeds(loadExtra float64, batch int) float64 {
	return cohereWeightsGB + loadExtra + 4*stretchGB + whisperFileGB*float64(batch)
}

func TestPlanForGPUWithRefine(t *testing.T) {
	// on Windows (WDDM): a process's budget is about 1.05 GiB short of the card, loading
	// the model beside Whisper takes 1.6 GiB more than its weights, and Whisper turbo
	// holds 1.0 GiB of it in float16 (0.55 in int8_float16) and 0.14 per file: measured
	// on the RTX 2080 SUPER (8 GB), which therefore keeps Whisper alone
	fakeRefineMin(t, 9.5)
	if _, fits := PlanForGPUWithRefine(8); fits || SecondListenFits(8) {
		t.Error("an 8 GB card cannot hold the second listen beside Whisper under Windows")
	}
	type machine struct {
		name                              string
		minGB, reserve, extra, fp16, int8 float64
		sizes                             []float64
	}
	for _, m := range []machine{
		{"windows", 9.5, 1.05, 1.6, 1.0, 0.55, []float64{80, 48, 40, 24, 20, 16, 12, 11, 10, 9.6}},
		// elsewhere: what the display keeps, a margin for loading, and Whisper's own
		// figures (2.3 GiB in float16, 1.2 in int8_float16, with CUDA's buffers)
		{"linux", 7.5, 0.3, 0.5, 2.3, 1.2, []float64{80, 40, 24, 16, 12, 11, 10, 8, 7.6}},
	} {
		fakeRefineMin(t, m.minGB)
		prev, _ := PlanForGPUWithRefine(1 << 10)
		for _, gb := range m.sizes {
			p, fits := PlanForGPUWithRefine(gb)
			if !fits || !p.ct2 || p.Workers < 1 || p.Batch < 1 {
				t.Fatalf("%s %v GB: %+v, %v", m.name, gb, p, fits)
			}
			whisper := m.fp16
			if p.ComputeType == "int8_float16" {
				whisper = m.int8
			}
			// every worker's Whisper (after a pass) and Cohere, then the last one's check
			perWorker := whisper + whisperFileGB*float64(p.Batch)
			loaded := cohereWeightsGB + m.extra
			free := gb - m.reserve - float64(p.Workers)*perWorker - float64(p.Workers-1)*loaded
			if need := secondListenNeeds(m.extra, p.Batch); free < need {
				t.Errorf("%s %v GB: %+v leaves %.2f GiB, the second listen needs %.2f", m.name, gb, p, free, need)
			}
			alone, _ := PlanForGPU(gb)
			if p.Workers*p.Batch > alone.Workers*alone.Batch {
				t.Errorf("%s %v GB: %+v is more than Whisper alone (%+v)", m.name, gb, p, alone)
			}
			if p.Workers > prev.Workers || p.Workers*p.Batch > prev.Workers*prev.Batch {
				t.Errorf("%s %v GB: %+v is not a step down from %+v", m.name, gb, p, prev)
			}
			prev = p
		}
	}
	fakeRefineMin(t, 7.5)
	if p, fits := PlanForGPUWithRefine(8); !fits || p.Batch != 4 || p.ComputeType != "int8_float16" {
		t.Errorf("an 8 GB card beside the second listen, outside Windows = %+v, %v", p, fits)
	}
	if _, fits := PlanForGPUWithRefine(6); fits {
		t.Error("a 6 GB card holds no second listen")
	}
}

func TestPlanSTTLeavesRoomForTheSecondListen(t *testing.T) {
	fakeMachine(t, false, 64)
	noSTTEnv(t)
	cfg := config.Defaults() // refine on
	fakeGPU(t, "NVIDIA GeForce RTX 3060, 12288\n", nil)
	if p := PlanSTT(cfg); p.Workers != 1 || p.Batch != 8 || p.ComputeType != "" {
		t.Errorf("12 GB with the second listen = %+v", p)
	}
	off := cfg
	off.Transcription.Refine = "off"
	if p := PlanSTT(off); p != PlanSTT(withGPUAlone(cfg)) || p.Batch != 16 {
		t.Errorf("12 GB, second listen off = %+v, want Whisper's tier alone", p)
	}
	t.Setenv("MAKTABA_STT_REFINE", "0") // the operator's own environment turns it off
	if p := PlanSTT(cfg); p.Batch != 16 {
		t.Errorf("MAKTABA_STT_REFINE=0: %+v", p)
	}
	t.Setenv("MAKTABA_STT_REFINE", "")
	// this PC's 8 GB card under Windows: no room for it, so Whisper keeps its own tier
	fakeGPU(t, rtx2080Super, nil)
	if p := PlanSTT(cfg); p.Batch != 8 || p.ComputeType != "" {
		t.Errorf("8 GB, Windows threshold = %+v", p)
	}
	fakeRefineMin(t, 7.5) // ... elsewhere it fits, Whisper made smaller
	if p := PlanSTT(cfg); p.Batch != 4 || p.ComputeType != "int8_float16" {
		t.Errorf("8 GB, Linux threshold = %+v", p)
	}
	c := cfg
	c.Transcription.Batch, c.Transcription.ComputeType = 6, "float16" // the owner's knobs still win
	if p := PlanSTT(c); p.Batch != 6 || p.ComputeType != "float16" {
		t.Errorf("[transcription] overrides beside the second listen = %+v", p)
	}
	m := envMap(mainProcess(t, cfg).Cmd.Env)
	if sttExclude(m) != "transcribe" {
		t.Errorf("the main worker leaves transcribe to the GPU worker: %v", m)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 1 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	if w := envMap(procs[0].Cmd.Env); w["MAKTABA_STT_BATCH"] != "4" || w["MAKTABA_STT_COMPUTE_TYPE"] != "int8_float16" {
		t.Errorf("the worker beside the second listen: %v", w)
	}
}

// defaultRefineMinGB is refineMinGB as this OS has it (the tests change it).
var defaultRefineMinGB = refineMinGB

// TestRefineThresholdIsThePipelines: the supervisor leaves room for the second listen
// on exactly the GPUs the pipeline runs it on (MIN_GPU_GB in stt/cohere_cuda.py).
func TestRefineThresholdIsThePipelines(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_pipeline", "stt", "cohere_cuda.py"))
	if err != nil {
		t.Skipf("the pipeline's source is not in this checkout: %v", err)
	}
	m := regexp.MustCompile(`(?m)^MIN_GPU_GB = ([0-9.]+) if sys\.platform == "win32" else ([0-9.]+)\r?$`).FindSubmatch(src)
	if m == nil {
		t.Fatal("stt/cohere_cuda.py: no MIN_GPU_GB line to compare with")
	}
	want := string(m[2])
	if runtime.GOOS == "windows" {
		want = string(m[1])
	}
	if got := strconv.FormatFloat(defaultRefineMinGB, 'f', -1, 64); got != want {
		t.Errorf("refineMinGB = %s on %s, the pipeline's MIN_GPU_GB = %s", got, runtime.GOOS, want)
	}
	cc := regexp.MustCompile(`(?m)^MIN_COMPUTE = \((\d+), (\d+)\)\r?$`).FindSubmatch(src)
	if cc == nil || string(cc[1]) != strconv.Itoa(refineMinComputeMajor) || string(cc[2]) != strconv.Itoa(refineMinComputeMinor) {
		t.Errorf("stt/cohere_cuda.py MIN_COMPUTE = %q, the supervisor's %d.%d", cc, refineMinComputeMajor, refineMinComputeMinor)
	}
	drv := regexp.MustCompile(`(?m)^MIN_DRIVER = (\d+)\r?$`).FindSubmatch(src)
	if drv == nil || string(drv[1]) != strconv.Itoa(refineMinDriver) {
		t.Errorf("stt/cohere_cuda.py MIN_DRIVER = %q, the supervisor's %d", drv, refineMinDriver)
	}
	// what each worker's second listen holds: WEIGHTS_GB + LOAD_EXTRA_GB + MIN_BATCH x STRETCH_GB
	num := func(pattern string, group int) float64 {
		t.Helper()
		m := regexp.MustCompile(`(?m)` + pattern).FindSubmatch(src)
		if m == nil {
			t.Fatalf("stt/cohere_cuda.py: no %s", pattern)
		}
		v, err := strconv.ParseFloat(string(m[group]), 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	extra := num(`^LOAD_EXTRA_GB = ([0-9.]+) if sys\.platform == "win32" else ([0-9.]+)\r?$`, 2)
	if runtime.GOOS == "windows" {
		extra = num(`^LOAD_EXTRA_GB = ([0-9.]+) if sys\.platform == "win32" else ([0-9.]+)\r?$`, 1)
	}
	cohere := num(`^WEIGHTS_GB = ([0-9.]+)\r?$`, 1) + extra + num(`^MIN_BATCH = (\d+)\r?$`, 1)*num(`^STRETCH_GB = ([0-9.]+)\r?$`, 1)
	if math.Abs(cohereGB-cohere) > 1e-9 {
		t.Errorf("cohereGB = %v on %s, the pipeline's %v", cohereGB, runtime.GOOS, cohere)
	}
	model, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "pipeline", "src", "maktaba_pipeline", "stt", "cohere.py"))
	if err != nil {
		t.Fatal(err)
	}
	if m := regexp.MustCompile(`(?m)^DEFAULT_MODEL = "([^"]+)"`).FindSubmatch(model); m == nil || string(m[1]) != defaultRefineModel {
		t.Errorf("stt/cohere.py DEFAULT_MODEL = %q, the supervisor's %s", m, defaultRefineModel)
	}
}

// Review of #209, item 4: the second listen's tiers (and its workers) only where it
// runs: memory, compute capability, driver, transcription on the GPU, its model.
func TestTheSecondListenNeedsTheWholeCapability(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 64)
	cfg := config.Defaults() // refine on
	for _, c := range []struct {
		smi     string
		workers int
		batch   int
	}{
		{"NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44\n", 1, 8},  // runs it: its tier
		{"NVIDIA GeForce RTX 3080 Ti, 12288\n", 1, 8},               // nvidia-smi does not say: runs it
		{"NVIDIA GeForce GTX 1080 Ti, 11264, 6.1, 591.44\n", 0, 16}, // Pascal: Whisper alone, no worker
		{"NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 572.16\n", 0, 16}, // driver before R580
		{"NVIDIA GeForce GTX 1080 Ti, 11264, 472.12\n", 0, 16},      // the old fallback's driver
	} {
		fakeGPU(t, c.smi, nil)
		if p := PlanSTT(cfg); p.Workers != c.workers || p.Batch != c.batch {
			t.Errorf("%q: %+v, want %d workers of %d files", c.smi, p, c.workers, c.batch)
		}
	}
	fakeGPU(t, "NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44\n", nil)
	secondListenModel = func(config.Config) bool { return false } // no token, no copy
	if p := PlanSTT(cfg); p.Workers != 0 || p.Batch != 16 {
		t.Errorf("no way to the model: %+v, want Whisper alone", p)
	}
	secondListenModel = func(config.Config) bool { return true }
	t.Setenv("MAKTABA_STT_DEVICE", "cpu")
	if secondListenRuns(cfg) {
		t.Error("transcription on the CPU (the operator's environment) has no second listen")
	}
	t.Setenv("MAKTABA_STT_DEVICE", "")
	cudaWhisper = func() bool { return false }
	if secondListenRuns(cfg) {
		t.Error("no CUDA Whisper here (Linux aarch64): no second listen")
	}
}

// secondListenModel looks where the pipeline looks for the model.
func TestTheSecondListensModelCanBeHad(t *testing.T) {
	noSTTEnv(t)
	dir := t.TempDir()
	for _, k := range []string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "HF_TOKEN_PATH", "HF_HOME", "MAKTABA_MODELS_DIR", "MAKTABA_STT_REFINE_MODEL"} {
		t.Setenv(k, "")
	}
	t.Setenv("MAKTABA_MODEL_CACHE_DIR", dir)
	cfg := config.Defaults()
	if secondListenModel(cfg) {
		t.Fatal("nothing here, no token: no model to be had")
	}
	hf := filepath.Join(dir, "huggingface")
	mk := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(hf)
	tokenFile := filepath.Join(hf, "token")
	if err := os.WriteFile(tokenFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !secondListenModel(cfg) {
		t.Error("a token in the pipeline's HF_HOME (hf auth login)")
	}
	_ = os.Remove(tokenFile)
	snap := filepath.Join(hf, "hub", "models--CohereLabs--cohere-transcribe-arabic-07-2026", "snapshots", "c3e911b4")
	mk(snap)
	if !secondListenModel(cfg) {
		t.Error("the model in the pipeline's Hugging Face cache")
	}
	_ = os.RemoveAll(filepath.Join(hf, "hub"))
	mk(filepath.Join(dir, "share-copies", "CohereLabs__cohere-transcribe-arabic-07-2026"))
	if !secondListenModel(cfg) {
		t.Error("a copy of the home's on this machine")
	}
	_ = os.RemoveAll(filepath.Join(dir, "share-copies"))
	shared := t.TempDir()
	mk(filepath.Join(shared, "CohereLabs__cohere-transcribe-arabic-07-2026"))
	t.Setenv("MAKTABA_MODELS_DIR", shared)
	if !secondListenModel(cfg) {
		t.Error("the share's copy (MAKTABA_MODELS_DIR)")
	}
	t.Setenv("MAKTABA_MODELS_DIR", "")
	cfg.Transcription.RefineModel = shared
	if !secondListenModel(cfg) {
		t.Error("a folder of the model")
	}
	cfg.Transcription.RefineModel = ""
	t.Setenv("HF_TOKEN", "hf_x")
	if !secondListenModel(cfg) {
		t.Error("HF_TOKEN")
	}
}

// TestSetupsSecondListenCheckIsTheSupervisors: setup asks whether this machine runs
// the second listen (#213, cmd/maktaba-server/second_listen.go) with the supervisor's
// thresholds. Skipped until that file is in this checkout.
func TestSetupsSecondListenCheckIsTheSupervisors(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "second_listen.go"))
	if err != nil {
		t.Skipf("setup's second-listen check is not in this checkout: %v", err)
	}
	read := func(name string) string {
		t.Helper()
		m := regexp.MustCompile(`(?m)\b` + name + `\s*=\s*([0-9.]+)`).FindSubmatch(src)
		if m == nil {
			t.Fatalf("second_listen.go: no %s", name)
		}
		return string(m[1])
	}
	gb := func(mib string) float64 {
		n, err := strconv.ParseFloat(mib, 64)
		if err != nil {
			t.Fatal(err)
		}
		return n / 1024
	}
	if w, o := gb(read("secondListenMinMiBWindows")), gb(read("secondListenMinMiB")); w != 9.5 || o != 7.5 ||
		(runtime.GOOS == "windows" && w != defaultRefineMinGB) || (runtime.GOOS != "windows" && o != defaultRefineMinGB) {
		t.Errorf("setup's memory thresholds %v (Windows) and %v GiB, the supervisor's %v on %s", w, o, defaultRefineMinGB, runtime.GOOS)
	}
	if got, want := read("secondListenMinCompute"), strconv.Itoa(refineMinComputeMajor)+"."+strconv.Itoa(refineMinComputeMinor); got != want {
		t.Errorf("setup's compute capability %s, the supervisor's %s", got, want)
	}
	if got := read("secondListenMinDriver"); got != strconv.Itoa(refineMinDriver) {
		t.Errorf("setup's driver %s, the supervisor's %d", got, refineMinDriver)
	}
}

// withGPUAlone is cfg with the second listen off.
func withGPUAlone(cfg config.Config) config.Config {
	cfg.Transcription.Refine = "off"
	return cfg
}

func TestPlanSTT(t *testing.T) {
	noSTTEnv(t)
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
	if p := PlanSTT(c); p.Workers != 3 || p.GPUGB != 9 || p.AudioHours != 1 {
		t.Errorf("[transcription] overrides = %+v (three workers share one's 3 h of audio)", p)
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
	noSTTEnv(t)
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
	numCPU = func() int { return 8 }
	totalMemory = func() uint64 { return 0 }
	if p := PlanSTT(cfg); p.AudioHours != 0 || p.MinFreePct != 0 {
		t.Errorf("unknown RAM leaves the audio budget to the pipeline: %+v", p)
	}
	totalMemory = func() uint64 { return 32 << 30 }

	c := cfg
	c.Pipeline.Instances = 2 // a Linux node's installer ran two workers
	if p := PlanSTT(c); p.Workers != 2 || p.Batch != 4 || p.CPUThreads != 2 || p.AudioHours != host.AudioHours/2 {
		t.Errorf("a Linux node's two instances = %+v: each half the threads and audio hours", p)
	}
	c = cfg
	c.Transcription.Batch, c.Transcription.CPUThreads, c.Transcription.ComputeType = 2, 6, "INT8_float32"
	c.Transcription.GPUGB, c.Transcription.AudioHours = 9, 5
	if p := PlanSTT(c); p.Batch != 2 || p.CPUThreads != 6 || p.ComputeType != "int8_float32" || p.GPUGB != 0 || p.AudioHours != 5 {
		t.Errorf("[transcription] overrides on the CPU = %+v (gpu_gb is MLX's)", p)
	}
	c.Transcription.Workers = 3 // the operator's threads are each worker's
	if p := PlanSTT(c); p.Workers != 3 || p.CPUThreads != 6 {
		t.Errorf("cpu_threads with three workers = %+v", p)
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
	noSTTEnv(t)
	cfg := noRefine()
	fakeMachine(t, false, 64)
	fakeGPU(t, rtx2080Super, nil)
	host := PlanForRAM(64)
	want := STTPlan{Workers: 1, Batch: 8, AudioHours: host.AudioHours, MinFreePct: host.MinFreePct, ct2: true}
	if p := PlanSTT(cfg); p != want {
		t.Errorf("RTX 2080 SUPER = %+v, want %+v", p, want)
	}
	fakeGPU(t, "NVIDIA GeForce RTX 4090, 24564\nNVIDIA GeForce RTX 3060, 12288\n", nil)
	if p := PlanSTT(withGPUAlone(cfg)); p.Workers != 2 || p.Batch != 16 {
		t.Errorf("RTX 4090 (the first GPU) = %+v", p)
	}
	on := cfg
	on.Transcription.Refine = "on"
	if p := PlanSTT(on); p.Workers != 1 || p.Batch != 32 {
		t.Errorf("RTX 4090 beside the second listen = %+v", p)
	}
	fakeGPU(t, "NVIDIA GeForce GTX 1650, 4096", nil)
	if p := PlanSTT(cfg); p.Workers != 1 || p.Batch != 4 || p.ComputeType != "int8_float16" {
		t.Errorf("4 GB GPU = %+v", p)
	}
	// a 2 or 3 GB card: the CPU, unless the operator asks for the GPU
	fakeGPU(t, "NVIDIA GeForce GTX 1060 3GB, 3072", nil)
	if p := PlanSTT(cfg); p.Workers != 0 || p.Batch != cpuBatch || p.CPUThreads != 4 || p.ComputeType != "" {
		t.Errorf("3 GB GPU = %+v, want the CPU plan", p)
	}
	c := cfg
	c.Transcription.Device = "cuda"
	if p := PlanSTT(c); p.Workers != 1 || p.Batch != 4 || p.ComputeType != "int8_float16" || p.Device != "cuda" {
		t.Errorf("3 GB GPU with device = cuda = %+v, want the smallest tier", p)
	}
	// Linux on aarch64: CTranslate2 has no CUDA build there
	fakeGPU(t, rtx2080Super, nil)
	cudaWhisper = func() bool { return false }
	if p := PlanSTT(cfg); p.Workers != 0 || p.Batch != cpuBatch || p.CPUThreads != 4 {
		t.Errorf("an NVIDIA GPU on aarch64 = %+v, want the CPU plan", p)
	}
	cudaWhisper = func() bool { return true }

	c = cfg
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

// Review of #201, item 1: with the second listen on, a machine that cannot run it
// starts no transcription workers of its own accord (the main worker transcribes
// only when the home's rules let it); one that can (Apple Silicon) does.
func TestWorkersWaitForTheSecondListen(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 32)
	fakeGPU(t, rtx2080Super, nil)
	cfg := config.Defaults() // refine on
	p := PlanSTT(cfg)
	if p.Workers != 0 || p.Batch != 8 || !p.ct2 {
		t.Errorf("an 8 GB GPU with the second listen on = %+v, want no workers and the GPU's knobs", p)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 0 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	main := envMap(mainProcess(t, cfg).Cmd.Env)
	if v := sttExclude(main); v != "" {
		t.Errorf("the main worker decides by the rules: exclude = %q", v)
	}
	if main["MAKTABA_STT_BATCH"] != "8" {
		t.Errorf("the main worker has the GPU's knobs for when the rules let it: %v", main)
	}
	t.Setenv("MAKTABA_STT_REFINE", "0") // the operator's own environment, as server.toml's refine
	if p := PlanSTT(cfg); p.Workers != 1 {
		t.Errorf("MAKTABA_STT_REFINE=0 = %+v, want the GPU's worker", p)
	}
	t.Setenv("MAKTABA_STT_REFINE", "")
	c := cfg
	c.Transcription.Workers = 1 // asked for: the workers run, and transcribe as the rules say
	if p := PlanSTT(c); p.Workers != 1 || p.Batch != 8 {
		t.Errorf("workers = 1 with the second listen on = %+v", p)
	}
	secondListen := secondListenRuns
	t.Cleanup(func() { secondListenRuns = secondListen })
	secondListenRuns = func(config.Config) bool { return true }
	if p := PlanSTT(cfg); p.Workers != 1 {
		t.Errorf("a GPU that runs the second listen = %+v, want its worker", p)
	}
	fakeMachine(t, true, 96)
	secondListenRuns = secondListen
	if p := PlanSTT(cfg); p.Workers != 2 {
		t.Errorf("Apple Silicon runs the second listen: %+v", p)
	}
}

// Review of #201, item 1: the operator's own stage selection stands.
func TestTheOperatorsExclusionStands(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, true, 96)
	cfg := config.Defaults()
	for _, env := range []struct{ exclude, only string }{
		{"index, Transcribe", ""}, // this PC's launcher: audio is the Mac's
		{"", "index,tag"},
	} {
		t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", env.exclude)
		t.Setenv("MAKTABA_PIPELINE_STAGES", env.only)
		for _, c := range []config.Config{cfg, func() config.Config { c := cfg; c.Pipeline.Instances = 2; return c }()} {
			if p := PlanSTT(c); p.Workers != 0 {
				t.Errorf("%+v: %+v, want no workers", env, p)
			}
			procs, err := BuildPipelineInstances(Options{Config: c})
			if err != nil || len(procs) != 0 {
				t.Errorf("%+v: %d workers, %v", env, len(procs), err)
			}
		}
		main := envMap(mainProcess(t, cfg).Cmd.Env)
		if main["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != env.exclude || main["MAKTABA_PIPELINE_STAGES"] != env.only {
			t.Errorf("the main worker's stages must be the operator's: %q / %q", main["MAKTABA_PIPELINE_EXCLUDE_STAGES"], main["MAKTABA_PIPELINE_STAGES"])
		}
	}
	// stages the operator excludes besides transcribe reach the workers as they are
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "index")
	t.Setenv("MAKTABA_PIPELINE_STAGES", "transcribe,index")
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 2 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	for _, p := range procs {
		if m := envMap(p.Cmd.Env); m["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index" || m["MAKTABA_PIPELINE_STAGES"] != "transcribe" {
			t.Errorf("%s: exclude %q, stages %q", p.Instance, m["MAKTABA_PIPELINE_EXCLUDE_STAGES"], m["MAKTABA_PIPELINE_STAGES"])
		}
	}
	if m := envMap(mainProcess(t, cfg).Cmd.Env); m["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index,transcribe" {
		t.Errorf("the main worker leaves transcribe to the workers: %q", m["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
}

// Review of #201, item 5: more workers than the plan's share its budget.
func TestMoreWorkersShareTheBudget(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 32)
	fakeGPU(t, rtx2080Super, nil)
	host := PlanForRAM(32)
	cfg := noRefine()
	for _, n := range []int{2, 3, 4} {
		c := cfg
		c.Transcription.Workers = n
		p := PlanSTT(c)
		if p.Workers != n || p.Batch < 1 || p.Batch > 8 || p.AudioHours != math.Round(host.AudioHours/float64(n)*100)/100 {
			t.Errorf("%d workers on 8 GB = %+v", n, p)
		}
		if need := gpuNeed(p, n); n < 4 && need > 8*gpuShare {
			t.Errorf("%d workers on 8 GB = %+v: %.1f GB", n, p, need)
		}
	}
	c := cfg
	c.Transcription.Workers = 3
	if p := PlanSTT(c); p.ComputeType != "int8_float16" || p.Batch != 4 {
		t.Errorf("three workers on 8 GB = %+v, want int8_float16 and four files each", p)
	}
	c.Transcription.ComputeType = "float16" // the operator's precision stands
	if p := PlanSTT(c); p.ComputeType != "float16" || p.Batch != 1 {
		t.Errorf("three float16 workers on 8 GB = %+v, want one file each", p)
	}
	c = cfg
	c.Transcription.Workers = 1 // fewer than the plan's: its own batch
	fakeGPU(t, "NVIDIA RTX A6000, 49140", nil)
	if p := PlanSTT(c); p.Workers != 1 || p.Batch != 32 {
		t.Errorf("one worker on 48 GB = %+v", p)
	}
	// MLX: each its part of the GPU memory
	fakeMachine(t, true, 96)
	c = config.Defaults()
	c.Transcription.Workers = 4
	if p := PlanSTT(c); p.GPUGB != 10 || p.GPUCacheGB != 1 || p.AudioHours != 8 {
		t.Errorf("four workers on the 96 GB Mac = %+v", p)
	}
}

// Review of #201, item 6: no workers where the pipeline cannot load Whisper.
func TestNoWorkersWithoutTheEngine(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 32)
	fakeGPU(t, rtx2080Super, nil)
	asked := ""
	sttInstalled = func(python string) bool { asked = python; return false }
	cfg := noRefine()
	cfg.Pipeline.Python = "/opt/maktaba/venv/bin/python"
	if p := PlanFor(Options{Config: cfg}); p.Workers != 0 || p.Batch != 8 {
		t.Errorf("a venv without the stt extra = %+v", p)
	}
	if asked != cfg.Pipeline.Python {
		t.Errorf("asked %q, want the pipeline's own Python", asked)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 0 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	if v := sttExclude(envMap(mainProcess(t, cfg).Cmd.Env)); v != "" {
		t.Errorf("no workers: the main worker keeps transcribe (and says why it cannot): %q", v)
	}
	sttInstalled = func(string) bool { return true }
	if p := PlanFor(Options{Config: cfg}); p.Workers != 1 {
		t.Errorf("with the stt extra = %+v", p)
	}
}

// The probe: only the pipeline's answer "not installed" (exit 3) holds the workers
// back, and each interpreter is asked once. The test binary stands in for Python (a
// child process: skipped under -short, as the other fake-child tests).
func TestSTTProbe(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes; runs without -short")
	}
	t.Cleanup(func() { sttProbes.Lock(); sttProbes.seen = nil; sttProbes.Unlock() })
	if a := probeSTT(filepath.Join(t.TempDir(), "no-python")); !a.installed || a.torch != "" {
		t.Errorf("an interpreter that cannot run is not an answer: the plan stands (%+v)", a)
	}
	t.Setenv("MAKTABA_FAKE_CHILD", "crash")
	for _, c := range []struct {
		code, out string
		want      sttAnswer
	}{
		{"3", "", sttAnswer{}},
		{"0", "", sttAnswer{installed: true}},
		{"0", "torch cuda", sttAnswer{installed: true, torch: torchCUDA}},
		{"0", "torch cpu", sttAnswer{installed: true, torch: torchCPU}},
		{"3", "torch absent", sttAnswer{torch: torchAbsent}},
		{"0", "torch unknown", sttAnswer{installed: true}},
	} {
		sttProbes.Lock()
		sttProbes.seen = nil
		sttProbes.Unlock()
		t.Setenv("FAKE_CODE", c.code)
		t.Setenv("FAKE_STDOUT", c.out)
		if got := probeSTT(os.Args[0]); got != c.want {
			t.Errorf("exit %s, %q: %+v, want %+v", c.code, c.out, got, c.want)
		}
		t.Setenv("FAKE_CODE", "3")
		t.Setenv("FAKE_STDOUT", "torch cpu")
		if got := probeSTT(os.Args[0]); got != c.want {
			t.Errorf("asked again after exit %s: %+v (the first answer stands)", c.code, got)
		}
	}
	if !strings.Contains(sttProbe, `"ctranslate2", "faster_whisper"`) || !strings.Contains(sttProbe, `"mlx_whisper"`) {
		t.Errorf("the probe must look for each engine's modules: %s", sttProbe)
	}
}

// Review of #209 (round 4): the venv's torch decides too. The default pin set
// installs PyPI's CPU torch on Windows: there the second listen gets no tiers and no
// workers, as nowhere else it cannot run. A probe that cannot say is not held
// against the machine.
func TestTheSecondListenNeedsTheVenvsCUDATorch(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 64)
	fakeGPU(t, "NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44\n", nil)
	secondListen := secondListenRuns
	t.Cleanup(func() { secondListenRuns = secondListen })
	secondListenRuns = func(config.Config) bool { return true } // the GPU and the model can
	cfg := config.Defaults()                                    // refine on
	alone, _ := PlanForGPU(12)
	beside, _ := PlanForGPUWithRefine(12)
	for _, c := range []struct {
		torch   string
		workers int
		batch   int
	}{
		{torchCUDA, beside.Workers, beside.Batch},
		{"", beside.Workers, beside.Batch}, // the probe could not say
		{torchCPU, 0, alone.Batch},         // Whisper's own batch, for the main worker
		{torchAbsent, 0, alone.Batch},
	} {
		asked := ""
		venvTorch = func(python string) string { asked = python; return c.torch }
		cfg.Pipeline.Python = "/opt/maktaba/venv/bin/python"
		if p := PlanFor(Options{Config: cfg}); p.Workers != c.workers || p.Batch != c.batch {
			t.Errorf("torch %q: %+v, want %d workers of %d files", c.torch, p, c.workers, c.batch)
		}
		if asked != cfg.Pipeline.Python {
			t.Errorf("torch %q: asked %q, want the pipeline's own Python", c.torch, asked)
		}
	}
	// setup's view (no venv yet) and Apple Silicon (MLX) do not ask
	venvTorch = func(string) string { t.Error("asked the venv's torch"); return torchCPU }
	if p := PlanSTT(cfg); p.Workers != beside.Workers {
		t.Errorf("without a venv to ask: %+v", p)
	}
	fakeMachine(t, true, 96)
	venvTorch = func(string) string { t.Error("asked torch on Apple Silicon"); return torchCPU }
	if p := PlanFor(Options{Config: config.Defaults()}); p.Workers != 2 {
		t.Errorf("Apple Silicon: %+v", p)
	}
}

// Review of #209 (round 4): more workers than the refine tier's each load Cohere too,
// so they share what is left of the card once each has it, not the tiers' third.
func TestMoreWorkersBesideTheSecondListenLeaveItsRoom(t *testing.T) {
	noSTTEnv(t)
	fakeMachine(t, false, 64)
	prevC, prevL := cohereGB, processLossGB
	t.Cleanup(func() { cohereGB, processLossGB = prevC, prevL })
	cohereGB, processLossGB = 5.81, 1 // Windows' figures
	secondListen := secondListenRuns
	t.Cleanup(func() { secondListenRuns = secondListen })
	secondListenRuns = func(config.Config) bool { return true }
	fakeGPU(t, "NVIDIA GeForce RTX 4090, 24564, 8.9, 591.44\n", nil)
	cfg := config.Defaults()
	cfg.Transcription.Workers = 2
	// (23 GiB / 2 - 5.81 - 2.3) / 0.14 = 24 files each, not the 32 a third of the card
	// would give Whisper alone
	if p := PlanSTT(cfg); p.Workers != 2 || p.Batch != 24 || p.ComputeType != "" {
		t.Errorf("two workers on 24 GB beside the second listen = %+v, want 24 files each", p)
	}
	cfg.Transcription.Workers = 3 // 1.86 GiB each beside Cohere: int8_float16, 4 files
	if p := PlanSTT(cfg); p.Batch != 4 || p.ComputeType != "int8_float16" {
		t.Errorf("three workers = %+v", p)
	}
	fakeGPU(t, "NVIDIA GeForce RTX 3080 Ti, 12288, 8.6, 591.44\n", nil)
	cfg.Transcription.Workers = 2 // not even a file each: one, and the pipeline's checks
	if p := PlanSTT(cfg); p.Batch != 1 {
		t.Errorf("two workers on 12 GB = %+v", p)
	}
	cfg = noRefine() // Whisper alone: the tiers' third of the card, as before
	cfg.Transcription.Workers = 3
	fakeGPU(t, "NVIDIA GeForce RTX 4090, 24564, 8.9, 591.44\n", nil)
	if p := PlanSTT(cfg); p.Workers != 3 || p.Batch != 16 {
		t.Errorf("three workers on 24 GB without the second listen = %+v", p)
	}
}

// mainProcess is the main pipeline worker Build makes for cfg.
func mainProcess(t *testing.T, cfg config.Config) Process {
	t.Helper()
	p, err := Build(RolePipeline, Options{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	return p
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
		{rtx2080Super, GPU{Name: "NVIDIA GeForce RTX 2080 SUPER", MemoryMiB: 8192}, true},
		{"\r\n  Tesla T4 , 15360 \r\n", GPU{Name: "Tesla T4", MemoryMiB: 15360}, true},
		{"NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 616.64\n", GPU{"NVIDIA GeForce RTX 2080 SUPER", 8192, 7, 5, "616.64"}, true},
		{"NVIDIA GeForce GTX 1080 Ti, 11264, 472.12\n", GPU{Name: "NVIDIA GeForce GTX 1080 Ti", MemoryMiB: 11264, Driver: "472.12"}, true},
		{"NVIDIA RTX A2000, 6138, [N/A], [N/A]\n", GPU{Name: "NVIDIA RTX A2000", MemoryMiB: 6138}, true},
		{"", GPU{}, false},
		{"Failed to initialize NVML: Driver/library version mismatch\n", GPU{}, false},
	} {
		if g, ok := parseNvidiaSMI([]byte(c.out)); g != c.want || ok != c.ok {
			t.Errorf("parse %q = %+v, %v", c.out, g, ok)
		}
	}
	if q := strings.Join(nvidiaSMIQuery, " "); q != "--query-gpu=name,memory.total,compute_cap,driver_version --format=csv,noheader,nounits" {
		t.Errorf("query = %s", q)
	}
	if q := strings.Join(nvidiaSMIFallback, " "); q != "--query-gpu=name,memory.total,driver_version --format=csv,noheader,nounits" {
		t.Errorf("fallback = %s", q)
	}
	if (GPU{Driver: "580.88"}).DriverMajor() != 580 || (GPU{}).DriverMajor() != 0 {
		t.Error("DriverMajor")
	}
}

func TestPipelineWorkersOnAppleSilicon(t *testing.T) {
	fakeMachine(t, true, 96)
	noSTTEnv(t)
	t.Setenv("MAKTABA_STT_GPU_GB", "30") // the operator's own wins
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "index")
	cfg := config.Defaults()
	main := envMap(mainProcess(t, cfg).Cmd.Env)
	if main["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index,transcribe" {
		t.Fatalf("the main worker must leave transcribe to the workers, keeping the operator's exclusions: %v", main["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
	if v := main["MAKTABA_STT_AUDIO_HOURS"]; v != "" {
		t.Errorf("the main worker does not transcribe: no knobs (%s)", v)
	}
	procs, err := BuildPipelineInstances(Options{Config: cfg})
	if err != nil || len(procs) != 2 {
		t.Fatalf("workers = %d, %v", len(procs), err)
	}
	for i, p := range procs {
		m := envMap(p.Cmd.Env)
		slot := "stt-" + string(rune('1'+i))
		if p.Instance != slot || m["MAKTABA_STT_SLOT"] != slot || m["MAKTABA_PIPELINE_STAGES"] != "transcribe" ||
			m["MAKTABA_PIPELINE_EXCLUDE_STAGES"] != "index" || m["MAKTABA_PIPELINE_GRPC_ADDR"] != "" {
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
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "")
	cfg.Transcription.Workers = -1
	m := envMap(mainProcess(t, cfg).Cmd.Env)
	if v := m["MAKTABA_STT_AUDIO_HOURS"]; v != "" {
		t.Errorf("Mac main worker without workers: %s", v)
	}
	if v := sttExclude(m); v != "" {
		t.Errorf("Mac main worker without workers transcribes: %q", v)
	}
}

func TestPipelineWorkersOnAnNvidiaGPU(t *testing.T) {
	fakeMachine(t, false, 32)
	fakeGPU(t, rtx2080Super, nil)
	noSTTEnv(t)
	t.Setenv("MAKTABA_STT_BATCH", "10") // the operator's own wins
	cfg := noRefine()
	cfg.Transcription.ComputeType = "int8_float16"
	main := envMap(mainProcess(t, cfg).Cmd.Env)
	if sttExclude(main) != "transcribe" {
		t.Errorf("the main worker leaves transcribe to the GPU worker: %q", main["MAKTABA_PIPELINE_EXCLUDE_STAGES"])
	}
	for _, k := range []string{"MAKTABA_STT_AUDIO_HOURS", "MAKTABA_STT_COMPUTE_TYPE", "MAKTABA_STT_CPU_THREADS"} {
		if v := main[k]; v != "" {
			t.Errorf("the main worker does not transcribe: %s=%s", k, v)
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
	m := envMap(mainProcess(t, cfg).Cmd.Env)
	host := PlanForRAM(16)
	if m["MAKTABA_STT_BATCH"] != "4" || m["MAKTABA_STT_CPU_THREADS"] != "4" || m["MAKTABA_STT_DEVICE"] != "cpu" ||
		m["MAKTABA_STT_AUDIO_HOURS"] != num(host.AudioHours) {
		t.Errorf("the main worker's knobs: %v", m)
	}
	if v := sttExclude(m); v != "" {
		t.Errorf("the main worker transcribes: exclude = %q", v)
	}
	t.Setenv("MAKTABA_STT_CPU_THREADS", "12")
	if m := envMap(mainProcess(t, cfg).Cmd.Env); m["MAKTABA_STT_CPU_THREADS"] != "12" {
		t.Errorf("the operator's own MAKTABA_STT_CPU_THREADS must be kept: %q", m["MAKTABA_STT_CPU_THREADS"])
	}
	if api := envMap(childEnvOK(RoleAPI, cfg)); api["MAKTABA_STT_BATCH"] != "" {
		t.Error("the transcription knobs are the pipeline's only")
	}
}

func TestTranscriptionEnv(t *testing.T) {
	for _, k := range []string{"MAKTABA_STT_MODEL", "MAKTABA_STT_REFINE", "MAKTABA_STT_REFINE_MODEL", "MAKTABA_PATH_MAP", "MAKTABA_WORKER_PATH_MAP"} {
		t.Setenv(k, "")
	}
	cfg := config.Defaults()
	cfg.Transcription.Model = "large-v3" // an old setup's short name: the pipeline's default
	m := envMap(childEnvOK(RolePipeline, cfg))
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
	m = envMap(childEnvOK(RolePipeline, cfg))
	if m["MAKTABA_STT_MODEL"] != cfg.Transcription.Model || m["MAKTABA_STT_REFINE"] != "0" ||
		m["MAKTABA_STT_REFINE_MODEL"] != cfg.Transcription.RefineModel || m["MAKTABA_WORKER_PATH_MAP"] != cfg.Media.PathMap {
		t.Errorf("pipeline env = %v", m)
	}
	if api := envMap(childEnvOK(RoleAPI, cfg)); api["MAKTABA_PATH_MAP"] != cfg.Media.PathMap || api["MAKTABA_STT_MODEL"] != "" {
		t.Errorf("api env: path map %q, stt model %q", api["MAKTABA_PATH_MAP"], api["MAKTABA_STT_MODEL"])
	}
	if _, ok := m["MAKTABA_PATH_MAP"]; ok {
		t.Error("the pipeline reads MAKTABA_PATH_MAP as Plex's map: it gets MAKTABA_WORKER_PATH_MAP only")
	}
	t.Setenv("MAKTABA_STT_MODEL", "own/model")
	t.Setenv("MAKTABA_PATH_MAP", "/own=/x")
	m = envMap(childEnvOK(RolePipeline, cfg))
	if _, ok := m["MAKTABA_STT_MODEL"]; ok {
		t.Error("the operator's own MAKTABA_STT_MODEL must be kept")
	}
	if _, ok := m["MAKTABA_PATH_MAP"]; ok {
		t.Error("the operator's own MAKTABA_PATH_MAP must be kept")
	}
}
