package supervisor

import (
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
)

// STTPlan is how this machine transcribes (Epic 36 story 36.4): Workers
// transcribe-only workers beside the main worker, each with these memory knobs
// (MAKTABA_STT_AUDIO_HOURS, _GPU_GB, _GPU_CACHE_GB, _MIN_FREE_PCT).
type STTPlan struct {
	Workers    int
	AudioHours float64
	GPUGB      float64
	GPUCacheGB float64
	MinFreePct float64
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

// appleSilicon reports a Mac on Apple Silicon (MLX transcription); tests replace it.
var appleSilicon = func() bool { return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" }

// PlanSTT is this machine's plan: [pipeline] instances when server.toml sets it
// (a converted node keeps its installer's workers), else [transcription]
// workers (-1: none), else on Apple Silicon sized from the RAM; elsewhere none
// (the one worker transcribes). [transcription] knobs override the tier's.
func PlanSTT(cfg config.Config) STTPlan {
	plan := STTPlan{} // elsewhere: no workers unless asked for, the pipeline's own knobs
	if appleSilicon() {
		plan = PlanForRAM(float64(totalMemory()) / (1 << 30))
	}
	t := cfg.Transcription
	switch {
	case cfg.PipelineInstancesSet || cfg.Pipeline.Instances > 0:
		plan.Workers = cfg.Pipeline.Instances
	case t.Workers < 0:
		plan.Workers = 0
	case t.Workers > 0:
		plan.Workers = t.Workers
	}
	for _, o := range []struct {
		v   float64
		dst *float64
	}{{t.AudioHours, &plan.AudioHours}, {t.GPUGB, &plan.GPUGB}, {t.GPUCacheGB, &plan.GPUCacheGB}, {t.MinFreePct, &plan.MinFreePct}} {
		if o.v > 0 {
			*o.dst = o.v
		}
	}
	return plan
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

// knobEnv is a transcription worker's memory knobs: the ones the plan sizes
// and the operator's environment does not set already.
func (p STTPlan) knobEnv() []string {
	var env []string
	for _, k := range []struct {
		name string
		v    float64
	}{{"MAKTABA_STT_AUDIO_HOURS", p.AudioHours}, {"MAKTABA_STT_GPU_GB", p.GPUGB},
		{"MAKTABA_STT_GPU_CACHE_GB", p.GPUCacheGB}, {"MAKTABA_STT_MIN_FREE_PCT", p.MinFreePct}} {
		if k.v > 0 && os.Getenv(k.name) == "" {
			env = append(env, k.name+"="+strconv.FormatFloat(k.v, 'f', -1, 64))
		}
	}
	return env
}
