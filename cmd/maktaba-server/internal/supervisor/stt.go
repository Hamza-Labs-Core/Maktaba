package supervisor

import (
	"os"
	"runtime"
	"strconv"
	"strings"

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
	{0, STTPlan{Workers: 1, Batch: 4, ComputeType: "int8_float16"}},
}

// PlanForGPU is the tier for a GPU with gb GiB of memory, Whisper alone on it.
func PlanForGPU(gb float64) STTPlan {
	for _, t := range gpuTiers {
		if gb >= t.minGB {
			p := t.plan
			p.ct2 = true
			return p
		}
	}
	p := gpuTiers[len(gpuTiers)-1].plan
	p.ct2 = true
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

// numCPU is the machine's logical CPUs; tests replace it.
var numCPU = runtime.NumCPU

// PlanSTT is this machine's plan: on Apple Silicon sized from the RAM (MLX); else
// (CTranslate2) from the NVIDIA GPU's memory, leaving room for the second listen
// when [transcription] refine is on and it fits there, or the CPU plan when there is
// no GPU or [transcription] device = "cpu", with the audio budget from the RAM. Then
// [pipeline] instances when server.toml sets it (a converted node keeps its
// installer's workers), else [transcription] workers (-1: none), set the number
// of workers, and the other [transcription] knobs override the plan's.
func PlanSTT(cfg config.Config) STTPlan {
	t := cfg.Transcription
	device := strings.ToLower(strings.TrimSpace(t.Device))
	var plan STTPlan
	if appleSilicon() {
		plan = PlanForRAM(float64(totalMemory()) / (1 << 30))
	} else {
		if gpu, ok := NvidiaGPU(); ok && device != "cpu" {
			plan = PlanForGPU(gpu.MemoryGB())
			if p, fits := PlanForGPUWithRefine(gpu.MemoryGB()); fits && refineOn(t) {
				plan = p
			}
		} else {
			plan = PlanForCPU(numCPU())
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
			plan.ComputeType = c
		}
		if t.CPUThreads > 0 {
			plan.CPUThreads = t.CPUThreads
		}
	}
	switch {
	case cfg.PipelineInstancesSet || cfg.Pipeline.Instances > 0:
		plan.Workers = cfg.Pipeline.Instances
	case t.Workers < 0:
		plan.Workers = 0
	case t.Workers > 0:
		plan.Workers = t.Workers
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
