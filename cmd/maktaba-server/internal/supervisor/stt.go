package supervisor

import (
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// STTPlan is how this machine transcribes: Workers transcribe-only workers beside
// the main worker, each with these knobs.
//
// Epic 36 story 36.4, Apple Silicon (MLX): memory knobs sized from the RAM
// (MAKTABA_STT_AUDIO_HOURS, _GPU_GB, _GPU_CACHE_GB, _MIN_FREE_PCT).
// Story 38.8, Windows and Linux (CTranslate2): workers, Batch and ComputeType sized
// from the NVIDIA GPU's memory, or on the CPU no workers (the main worker
// transcribes) with a CPU batch and CPUThreads; the host's audio budget from the RAM.
// Story 38.9: with the second listen on and a GPU it fits on, the tiers leave it room.
type STTPlan struct {
	Workers    int
	AudioHours float64
	GPUGB      float64
	GPUCacheGB float64
	MinFreePct float64
	// Batch: files decoded together, one 30 s window of each per pass (MAKTABA_STT_BATCH).
	Batch int
	// ComputeType: CTranslate2's precision (MAKTABA_STT_COMPUTE_TYPE); "": the pipeline's
	// choice (float16 on CUDA, int8 on the CPU).
	ComputeType string
	// Device: MAKTABA_STT_DEVICE ("cuda", "cuda:1", "cpu"); "": the pipeline uses the
	// GPU when CUDA is usable there, else the CPU.
	Device string
	// CPUThreads: CTranslate2's threads on the CPU (MAKTABA_STT_CPU_THREADS).
	CPUThreads int

	// ct2: a plan for the CTranslate2 engine (not Apple Silicon): its knobs reach the
	// main worker too when it transcribes itself
	ct2 bool
}

// sttTiers size the plan from the RAM, largest first. The top tier is what the
// 96 GB home Mac runs (tools/dev/stack.sh): two workers stay near a third of
// the memory and back off on their own below 25% free.
var sttTiers = []struct {
	minGB float64
	plan  STTPlan
}{
	{96, STTPlan{Workers: 2, AudioHours: 16, GPUGB: 20, GPUCacheGB: 2, MinFreePct: 25}},
	{64, STTPlan{Workers: 2, AudioHours: 10, GPUGB: 12, GPUCacheGB: 2, MinFreePct: 25}},
	{36, STTPlan{Workers: 1, AudioHours: 8, GPUGB: 12, GPUCacheGB: 2, MinFreePct: 25}},
	{24, STTPlan{Workers: 1, AudioHours: 6, GPUGB: 8, GPUCacheGB: 1, MinFreePct: 25}},
	{16, STTPlan{Workers: 1, AudioHours: 3, GPUGB: 5, GPUCacheGB: 1, MinFreePct: 20}},
	{0, STTPlan{Workers: 1, AudioHours: 2, GPUGB: 3, GPUCacheGB: 0.5, MinFreePct: 15}},
}

// PlanForRAM is the tier for totalGB of memory.
func PlanForRAM(totalGB float64) STTPlan {
	for _, t := range sttTiers {
		if totalGB >= t.minGB {
			return t.plan
		}
	}
	return sttTiers[len(sttTiers)-1].plan
}

// gpuTiers size a CTranslate2 plan from the NVIDIA GPU's memory (GiB), largest
// first. Measured on an RTX 2080 SUPER (8 GB, story 38.8): a worker's Whisper
// turbo and CUDA's own buffers take 2.3 GB in float16 (1.2 GB in int8_float16),
// each file of its batch 0.14 GB more while its window is encoded; a batch of 8
// ran at 72x real time and one of 12 no faster (69x), and with other programs on
// the GPU 12 ran out of room and slowed the encoder threefold. The tiers leave at
// least a third of the card to the display and the other programs. One worker
// keeps a GPU busy (it decodes a window of every file in flight as one batch); a
// second only pays on a large card, where it decodes while the first does its
// CPU work.
var gpuTiers = []struct {
	minGB float64
	plan  STTPlan
}{
	{40, STTPlan{Workers: 2, Batch: 32}},
	{20, STTPlan{Workers: 2, Batch: 16}},
	{14, STTPlan{Workers: 1, Batch: 24}},
	{10, STTPlan{Workers: 1, Batch: 16}},
	{7, STTPlan{Workers: 1, Batch: 8}},
	{5, STTPlan{Workers: 1, Batch: 8, ComputeType: "int8_float16"}},
	{gpuMinGB, STTPlan{Workers: 1, Batch: 4, ComputeType: "int8_float16"}},
}

// gpuMinGB is the smallest NVIDIA GPU (GiB) Whisper is planned on: a 4 GB card
// holds the int8_float16 model and a batch of four (1.8 GB) with a third of it left
// to the display. A smaller one gets the CPU plan: on a 2 or 3 GB card the model
// ran out of memory as it loaded, every file. (The pipeline checks the GPU's free
// memory before it loads the model too, and remembers a failed load.)
const gpuMinGB = 4

// What a CTranslate2 worker takes on the GPU (measured, story 38.8): the model and
// CUDA's own buffers by precision, and each file of its batch while its window is
// encoded. The tiers keep a GPU's workers within gpuShare of its memory.
const (
	ct2FileGB = 0.14
	gpuShare  = 2.0 / 3
)

var ct2ModelGB = map[string]float64{"": 2.3, "float16": 2.3, "int8_float16": 1.2}

// PlanForGPU is the tier for a GPU with gb GiB of memory, Whisper alone on it; false
// below gpuMinGB.
func PlanForGPU(gb float64) (STTPlan, bool) {
	for _, t := range gpuTiers {
		if gb >= t.minGB {
			p := t.plan
			p.ct2 = true
			return p, true
		}
	}
	return STTPlan{}, false
}

// shareGPU sizes each of n workers on a GPU of gb GiB, the plan p sized for fewer:
// the batch that keeps the n of them within the tiers' share of the GPU (at most
// p's own), in int8_float16 when float16 leaves no room for a batch of one (unless
// the operator named the precision). At least one file each: workers that still do
// not fit find the GPU full when they load, and the pipeline puts them on the CPU.
func shareGPU(p STTPlan, gb float64, n int, ownPrecision bool) STTPlan {
	per := gb * gpuShare / float64(n)
	fit := func(ct string) int {
		model, ok := ct2ModelGB[ct]
		if !ok {
			model = ct2ModelGB[""]
		}
		return int((per - model) / ct2FileGB)
	}
	batch := fit(p.ComputeType)
	if batch < 1 && !ownPrecision && p.ComputeType == "" {
		p.ComputeType = "int8_float16"
		batch = fit(p.ComputeType)
	}
	p.Batch = max(1, min(p.Batch, batch))
	return p
}

// refineMinGB is the smallest NVIDIA GPU (GiB) the second listen (Cohere Transcribe
// through torch, story 38.9) fits on beside Whisper: the pipeline's own threshold
// (MIN_GPU_GB in stt/cohere_cuda.py), from what it took on the RTX 2080 SUPER. On
// Windows the driver's budget (WDDM) leaves a process about 1 GiB short of the card,
// and loading the model beside Whisper takes 1.6 GiB more than its 3.85 GiB of
// weights: an 8 GB card has 5.8 GiB left once Whisper is loaded, short of the 6.4 the
// model, a batch of its stretches and Whisper's room need. Elsewhere an 8 GB card
// holds both. Tests replace it.
var refineMinGB = func() float64 {
	if runtime.GOOS == "windows" {
		return 9.5
	}
	return 7.5
}()

// SecondListenFits reports whether the second listen fits beside Whisper on an
// NVIDIA GPU of gb GiB (on a smaller one the pipeline keeps Whisper's words and says
// why, and does not download its model).
func SecondListenFits(gb float64) bool { return gb >= refineMinGB }

// refineGPUTiers size a CTranslate2 plan when the second listen runs on the same GPU
// (refine on, SecondListenFits): each worker then holds Cohere's weights, what
// loading them takes besides, a batch of its stretches (0.09 GiB each) and Whisper,
// whose batch the pipeline leaves room for (0.14 GiB a file). Each tier passes the
// pipeline's check (free memory before the model loads >= 3.85 + 1.6 + 0.36 +
// 0.14 x batch GiB) on an otherwise idle GPU, Windows' budget counted: so Whisper's
// batch is smaller than alone. The last tier is reached only where an 8 GB card
// fits (not Windows): Whisper in int8_float16, four files.
var refineGPUTiers = []struct {
	minGB float64
	plan  STTPlan
}{
	{40, STTPlan{Workers: 2, Batch: 24}},
	{20, STTPlan{Workers: 1, Batch: 32}},
	{14, STTPlan{Workers: 1, Batch: 16}},
	{11, STTPlan{Workers: 1, Batch: 8}},
	{9.5, STTPlan{Workers: 1, Batch: 4}},
	{0, STTPlan{Workers: 1, Batch: 4, ComputeType: "int8_float16"}},
}

// PlanForGPUWithRefine is the tier for a GPU with gb GiB of memory that Whisper
// shares with the second listen; false when the second listen does not fit on it
// (Whisper then has the GPU to itself: PlanForGPU).
func PlanForGPUWithRefine(gb float64) (STTPlan, bool) {
	if !SecondListenFits(gb) {
		return STTPlan{}, false
	}
	for _, t := range refineGPUTiers {
		if gb >= t.minGB {
			p := t.plan
			p.ct2 = true
			return p, true
		}
	}
	return STTPlan{}, false
}

// cpuBatch is the CPU plan's batch: a pass of four windows keeps CTranslate2's
// matrix products large enough to use the cores (2.6x real time against 2.0x one
// file at a time, on 10 threads of the story 38.8 PC) without a file waiting long
// for its next window.
const cpuBatch = 4

// PlanForCPU is the plan without a usable GPU: no transcription workers (the main
// worker transcribes), a CPU batch, and half the logical CPUs (2 to 16) for
// CTranslate2, the rest left to the other stages, streaming and the api.
func PlanForCPU(cpus int) STTPlan {
	return STTPlan{Batch: cpuBatch, CPUThreads: min(16, max(2, cpus/2)), ct2: true}
}

// appleSilicon reports a Mac on Apple Silicon (MLX transcription); tests replace it.
var appleSilicon = func() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// cudaWhisper reports a machine whose stt extra installs CTranslate2 with CUDA:
// Windows and Linux on x86_64 (its wheels for Linux aarch64 run on the CPU only, and
// the extra does not install them there). Elsewhere an NVIDIA GPU gets the CPU plan.
// Tests replace it.
var cudaWhisper = func() bool { return runtime.GOARCH == "amd64" }

// numCPU is the machine's logical CPUs; tests replace it.
var numCPU = runtime.NumCPU

// PlanSTT is this machine's plan: on Apple Silicon sized from the RAM (MLX); else
// (CTranslate2) from the NVIDIA GPU's memory, or the CPU plan when there is none (or
// one under gpuMinGB, or no CUDA build of CTranslate2 for the machine) or
// [transcription] device = "cpu", with the audio budget from the RAM.
//
// The workers it sizes run only where the second listen runs too, while
// [transcription] refine is on (secondListenRuns): elsewhere the main worker
// transcribes, and only when the home's rules let a machine without the second
// listen do so (rules transcription, without_second_listen), so by default the
// machines that can run it (the Mac) transcribe the home's files.
//
// Then [pipeline] instances when server.toml sets it (a converted node keeps its
// installer's workers), else [transcription] workers (-1: none), set the number of
// workers: more than the plan's share its budget (each its part of the GPU, the
// threads, the audio hours). The other [transcription] knobs override the plan's.
// The operator's own stage selection always stands: with transcribe excluded
// (operatorKeepsTranscribeOff) there are no workers.
func PlanSTT(cfg config.Config) STTPlan {
	t := cfg.Transcription
	device := strings.ToLower(strings.TrimSpace(t.Device))
	ownPrecision := false
	var plan STTPlan
	gpuGB := 0.0
	if appleSilicon() {
		plan = PlanForRAM(float64(totalMemory()) / (1 << 30))
	} else {
		fits := false
		if gpu, ok := NvidiaGPU(); ok && device != "cpu" && cudaWhisper() {
			gpuGB = gpu.MemoryGB()
			if plan, fits = PlanForGPU(gpuGB); !fits && strings.HasPrefix(device, "cuda") {
				// asked for: the smallest tier (the pipeline checks the free memory)
				plan, fits = PlanForGPU(gpuMinGB)
			}
			if p, ok := PlanForGPUWithRefine(gpuGB); ok && fits && refineOn(t) {
				plan = p // story 38.9: room for the second listen
			}
		}
		if !fits {
			plan, gpuGB = PlanForCPU(numCPU()), 0
		}
		// the decoded audio and its spectrogram are in the host's memory here
		if ram := totalMemory(); ram > 0 {
			host := PlanForRAM(float64(ram) / (1 << 30))
			plan.AudioHours, plan.MinFreePct = host.AudioHours, host.MinFreePct
		}
		if device != "" && device != "auto" {
			plan.Device = device
		}
		if c := strings.ToLower(strings.TrimSpace(t.ComputeType)); c != "" && c != "auto" {
			plan.ComputeType, ownPrecision = c, true
		}
		if t.CPUThreads > 0 {
			plan.CPUThreads = t.CPUThreads
		}
	}
	// the budget is the plan's own workers' (the main worker's, without any)
	base := max(1, plan.Workers)
	if refineOn(t) && !secondListenRuns() {
		plan.Workers = 0
	}
	switch {
	case cfg.PipelineInstancesSet || cfg.Pipeline.Instances > 0:
		plan.Workers = cfg.Pipeline.Instances
	case t.Workers < 0:
		plan.Workers = 0
	case t.Workers > 0:
		plan.Workers = t.Workers
	}
	if operatorKeepsTranscribeOff() {
		plan.Workers = 0
	}
	if n := plan.Workers; n > base {
		plan = shareBudget(plan, base, n, gpuGB, ownPrecision, t.CPUThreads > 0)
	}
	if t.Batch > 0 {
		plan.Batch = t.Batch
	}
	if t.AudioHours > 0 {
		plan.AudioHours = t.AudioHours
	}
	if t.MinFreePct > 0 {
		plan.MinFreePct = t.MinFreePct
	}
	if !plan.ct2 { // MLX's own GPU memory limits: CTranslate2 has none
		if t.GPUGB > 0 {
			plan.GPUGB = t.GPUGB
		}
		if t.GPUCacheGB > 0 {
			plan.GPUCacheGB = t.GPUCacheGB
		}
	}
	return plan
}

// shareBudget divides a plan sized for base workers among n: each its part of the
// host's audio hours and of MLX's GPU memory; on CTranslate2 each its part of the
// GPU (shareGPU) or of the CPU threads (unless the operator set them).
func shareBudget(p STTPlan, base, n int, gpuGB float64, ownPrecision, ownThreads bool) STTPlan {
	part := func(v float64) float64 { return math.Round(v*float64(base)/float64(n)*100) / 100 }
	p.AudioHours = part(p.AudioHours)
	p.GPUGB, p.GPUCacheGB = part(p.GPUGB), part(p.GPUCacheGB)
	switch {
	case p.ct2 && gpuGB > 0:
		p = shareGPU(p, gpuGB, n, ownPrecision)
	case p.ct2 && !ownThreads:
		p.CPUThreads = max(1, p.CPUThreads*base/n)
	}
	return p
}

// secondListenRuns reports whether this machine runs the second listen (Cohere
// Transcribe Arabic): on Apple Silicon (MLX, Epic 36 story 36.4), and on an NVIDIA
// GPU it fits on beside Whisper (story 38.9). Tests replace it.
var secondListenRuns = func() bool {
	if appleSilicon() {
		return true
	}
	gpu, ok := NvidiaGPU()
	return ok && cudaWhisper() && SecondListenFits(gpu.MemoryGB())
}

// refineOn reports whether the workers run the second listen: the operator's own
// MAKTABA_STT_REFINE when set (the pipeline's "0" is off), else [transcription]
// refine, on unless it says off.
func refineOn(t config.TranscriptionSection) bool {
	if v := strings.TrimSpace(os.Getenv("MAKTABA_STT_REFINE")); v != "" {
		return v != "0"
	}
	switch strings.ToLower(strings.TrimSpace(t.Refine)) {
	case "off", "false", "no", "0":
		return false
	}
	return true
}

// operatorKeepsTranscribeOff reports whether the operator's own environment keeps
// the transcribe stage off this machine, as the pipeline reads it:
// MAKTABA_PIPELINE_EXCLUDE_STAGES names it, or MAKTABA_PIPELINE_STAGES lists stages
// without it. The supervisor never overrides that: no transcription workers (a
// launcher sets it to leave audio to the Mac).
func operatorKeepsTranscribeOff() bool {
	if hasStage(os.Getenv("MAKTABA_PIPELINE_EXCLUDE_STAGES"), "transcribe") {
		return true
	}
	only := strings.TrimSpace(os.Getenv("MAKTABA_PIPELINE_STAGES"))
	return only != "" && !hasStage(only, "transcribe")
}

// hasStage reports whether a comma-separated stage list names stage.
func hasStage(list, stage string) bool {
	for _, s := range strings.Split(list, ",") {
		if strings.ToLower(strings.TrimSpace(s)) == stage {
			return true
		}
	}
	return false
}

// PlanFor is PlanSTT for the pipeline that opts starts: no workers when its Python
// does not have this machine's Whisper engine (a venv without the stt extra), where
// they would sit idle.
func PlanFor(opts Options) STTPlan {
	plan := PlanSTT(opts.Config)
	if plan.Workers > 0 && !sttInstalled(pythonFor(opts)) {
		plan.Workers = 0
	}
	return plan
}

// sttProbe asks the pipeline's Python whether it has this machine's Whisper engine
// (the stt extra: MLX-Whisper on Apple Silicon, CTranslate2 and faster-whisper
// elsewhere), without importing it: exit 3 when it has not.
const sttProbe = `import importlib.util as u, platform, sys
mlx = platform.system() == "Darwin" and platform.machine() == "arm64"
mods = ("numpy", "mlx_whisper") if mlx else ("numpy", "ctranslate2", "faster_whisper")
sys.exit(0 if all(u.find_spec(m) for m in mods) else 3)`

// sttProbeTimeout bounds the probe: an interpreter's first start can be slow (a
// virus scanner reading it), and the supervisor starts anyway.
const sttProbeTimeout = 30 * time.Second

var sttProbes struct {
	sync.Mutex
	seen map[string]bool
}

// sttInstalled reports whether python has the Whisper engine (probeSTT); tests
// replace it.
var sttInstalled = probeSTT

// probeSTT asks python whether it has the Whisper engine, once per interpreter. Only
// its answer "not installed" counts: a probe that cannot run (a timeout, an
// interpreter that fails) leaves the plan as it is, and the pipeline says why it
// cannot transcribe.
func probeSTT(python string) bool {
	sttProbes.Lock()
	defer sttProbes.Unlock()
	if ok, done := sttProbes.seen[python]; done {
		return ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), sttProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "-c", sttProbe)
	hideWindow(cmd)
	err := cmd.Run()
	var exit *exec.ExitError
	ok := !(errors.As(err, &exit) && exit.ExitCode() == 3)
	if sttProbes.seen == nil {
		sttProbes.seen = map[string]bool{}
	}
	sttProbes.seen[python] = ok
	return ok
}

// mainEnv is the main worker's share of the plan: with transcription workers it
// leaves transcribe to them (added to the stages the operator's own environment
// excludes); without them on the CTranslate2 engine it gets their knobs, for when
// it transcribes itself.
func (p STTPlan) mainEnv(env []string) []string {
	if p.Workers > 0 {
		env = setEnv(env, "MAKTABA_PIPELINE_EXCLUDE_STAGES", withStage(os.Getenv("MAKTABA_PIPELINE_EXCLUDE_STAGES"), "transcribe"))
	}
	return append(env, p.mainKnobEnv()...)
}

// sttEnv is the transcription settings every pipeline process gets: the models
// from [transcription] (MAKTABA_STT_MODEL, MAKTABA_STT_REFINE,
// MAKTABA_STT_REFINE_MODEL; unset keeps the pipeline's defaults).
func sttEnv(cfg config.Config) []string {
	var env []string
	t := cfg.Transcription
	// an old short name (setup wrote "large-v3") is not a model the pipeline loads
	if m := strings.TrimSpace(t.Model); strings.Contains(m, "/") {
		env = append(env, "MAKTABA_STT_MODEL="+m)
	}
	switch strings.ToLower(strings.TrimSpace(t.Refine)) {
	case "off", "false", "no", "0":
		env = append(env, "MAKTABA_STT_REFINE=0")
	case "on", "true", "yes", "1":
		env = append(env, "MAKTABA_STT_REFINE=1")
	}
	if m := strings.TrimSpace(t.RefineModel); m != "" {
		env = append(env, "MAKTABA_STT_REFINE_MODEL="+m)
	}
	return env
}

// knobEnv is a transcribing worker's knobs: the ones the plan sizes and the
// operator's environment does not set already.
func (p STTPlan) knobEnv() []string {
	var env []string
	add := func(name, v string) {
		if v != "" && os.Getenv(name) == "" {
			env = append(env, name+"="+v)
		}
	}
	num := func(v float64) string {
		if v <= 0 {
			return ""
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	add("MAKTABA_STT_AUDIO_HOURS", num(p.AudioHours))
	add("MAKTABA_STT_GPU_GB", num(p.GPUGB))
	add("MAKTABA_STT_GPU_CACHE_GB", num(p.GPUCacheGB))
	add("MAKTABA_STT_MIN_FREE_PCT", num(p.MinFreePct))
	add("MAKTABA_STT_BATCH", num(float64(p.Batch)))
	add("MAKTABA_STT_DEVICE", p.Device)
	add("MAKTABA_STT_COMPUTE_TYPE", p.ComputeType)
	add("MAKTABA_STT_CPU_THREADS", num(float64(p.CPUThreads)))
	return env
}

// mainKnobEnv is the main worker's share of the plan: the knobs when it is the
// one that transcribes on the CTranslate2 engine (no transcription workers). On
// Apple Silicon it keeps the pipeline's own, as before.
func (p STTPlan) mainKnobEnv() []string {
	if p.Workers > 0 || !p.ct2 {
		return nil
	}
	return p.knobEnv()
}
