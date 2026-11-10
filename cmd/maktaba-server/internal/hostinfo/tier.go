package hostinfo

// Hardware tiers (Epic 59 story 59.04, design §6.4): Light, Standard and Strong, set by the
// memory limit alone. A tier sets defaults, never limits: every setting it drives stays
// changeable, and a setting the household set keeps its value. A GPU changes single
// defaults (a CUDA GPU the speech model, a video device the transcode limits), never the
// tier.

import (
	"math"
	"strconv"
	"strings"
)

// Tier is a hardware tier.
type Tier string

// The tiers.
const (
	TierLight    Tier = "light"
	TierStandard Tier = "standard"
	TierStrong   Tier = "strong"
)

// Rank orders the tiers (Light 1, Standard 2, Strong 3; 0: not a tier).
func (t Tier) Rank() int {
	switch t {
	case TierLight:
		return 1
	case TierStandard:
		return 2
	case TierStrong:
		return 3
	}
	return 0
}

// ParseTier reads a tier's name ("" and anything else: false).
func ParseTier(s string) (Tier, bool) {
	t := Tier(strings.ToLower(strings.TrimSpace(s)))
	return t, t.Rank() > 0
}

// The memory rules, in bytes.
const (
	GiB = uint64(1) << 30
	// MinMemory is setup's floor: a "4 GB" NAS reports about 3.6–3.8 GiB after the
	// kernel's share, and the epic promises 4 GB models.
	MinMemory = 7 * GiB / 2
	// LightBelow, LightBelowARM and StrongFrom are the tiers' edges.
	LightBelow    = 6 * GiB
	LightBelowARM = 8 * GiB
	StrongFrom    = 16 * GiB
)

// TierFor is the tier of a memory limit (Machine.Memory.LimitBytes) on arch: Light under
// 6 GiB (8 GiB on arm64), Standard under 16 GiB, Strong from 16 GiB. An unknown limit (0)
// is Strong: today's defaults.
func TierFor(limit uint64, arch string) Tier {
	switch {
	case limit == 0:
		return TierStrong
	case limit < LightBelow, arch == "arm64" && limit < LightBelowARM:
		return TierLight
	case limit < StrongFrom:
		return TierStandard
	}
	return TierStrong
}

// Speech models the tiers pick (faster-whisper names).
const (
	ModelBase  = "base"
	ModelSmall = "small"
	ModelTurbo = "large-v3-turbo"
)

// turboGPUMiB is the GPU memory large-v3-turbo needs on a Light machine's GPU.
const turboGPUMiB = 4096

// Defaults is what a tier sets. A zero number means today's default (no cap).
type Defaults struct {
	Tier Tier `json:"tier"`
	// Postgres memory (59.02): shared_buffers, work_mem and maintenance_work_mem. On
	// Strong, shared_buffers is SharedBuffersPct of the memory limit, at most 4 GB.
	SharedBuffers      string `json:"shared_buffers"`
	SharedBuffersPct   int    `json:"shared_buffers_pct,omitempty"`
	WorkMem            string `json:"work_mem"`
	MaintenanceWorkMem string `json:"maintenance_work_mem"`
	// PipelineWorkers caps the pipeline's worker processes, the main worker counted
	// (0: today's): Light's one main worker transcribes too.
	PipelineWorkers int `json:"pipeline_workers"`
	// Transcodes in software (with their tallest output, 0: any) and on a working
	// hardware encoder (59.05; 0: as many as the GPU takes).
	SoftwareTranscodes int `json:"software_transcodes"`
	SoftwareMaxHeight  int `json:"software_max_height"`
	HardwareTranscodes int `json:"hardware_transcodes"`
	// Speech to text (59.06): on by default, and the model.
	SpeechOn    bool   `json:"speech_on"`
	SpeechModel string `json:"speech_model"`
	// QuietHours is when heavy work runs (59.02; "": any time); SpritesInQuietHours
	// holds thumbnail sprites to it.
	QuietHours          string `json:"quiet_hours"`
	SpritesInQuietHours bool   `json:"sprites_in_quiet_hours"`
}

// DefaultQuietHours is Light's and Standard's window.
const DefaultQuietHours = "01:00-06:00"

// DefaultsFor is what tier t sets on machine m (the GPU and CPU features adjust speech).
func DefaultsFor(t Tier, m Machine) Defaults {
	cuda := m.CUDAMiB()
	switch t {
	case TierLight:
		d := Defaults{Tier: t, SharedBuffers: "256MB", WorkMem: "4MB", MaintenanceWorkMem: "64MB",
			PipelineWorkers: 1, SoftwareTranscodes: 1, SoftwareMaxHeight: 720, HardwareTranscodes: 2,
			SpeechModel: ModelBase, QuietHours: DefaultQuietHours, SpritesInQuietHours: true}
		if cuda >= turboGPUMiB {
			d.SpeechModel = ModelTurbo
		}
		return d
	case TierStandard:
		d := Defaults{Tier: t, SharedBuffers: "512MB", WorkMem: "8MB", MaintenanceWorkMem: "128MB",
			PipelineWorkers: 2, SoftwareTranscodes: 2, SoftwareMaxHeight: 1080, HardwareTranscodes: 4,
			SpeechOn: true, SpeechModel: ModelSmall, QuietHours: DefaultQuietHours}
		switch {
		case cuda > 0:
			d.SpeechModel = ModelTurbo
		case !m.CPU.AVX2:
			d.SpeechModel = ModelBase
		}
		return d
	}
	d := Defaults{Tier: TierStrong, SharedBuffersPct: 25, WorkMem: "16MB", MaintenanceWorkMem: "256MB",
		SpeechOn: true, SpeechModel: ModelSmall}
	d.SharedBuffers = StrongSharedBuffers(m.Memory.LimitBytes)
	if cuda > 0 || m.CPU.AVX2 {
		d.SpeechModel = ModelTurbo
	}
	return d
}

// StrongSharedBuffers is 25% of the memory limit, at most 4 GB, in MB ("4096MB"); an
// unknown limit gives Postgres's own default ("128MB").
func StrongSharedBuffers(limit uint64) string {
	if limit == 0 {
		return "128MB"
	}
	mb := limit / 4 >> 20
	if mb > 4096 {
		mb = 4096
	}
	if mb < 128 {
		mb = 128
	}
	return formatMB(mb)
}

func formatMB(mb uint64) string { return strconv.FormatUint(mb, 10) + "MB" }

// CUDAMiB is the memory of the largest NVIDIA GPU (0: none).
func (m Machine) CUDAMiB() uint64 {
	var best uint64
	for _, g := range m.GPU.NVIDIA {
		if g.MemoryMB > best {
			best = g.MemoryMB
		}
	}
	return best
}

// VideoDevice names the device video could use, for the tier's reason: the NVIDIA GPU,
// else the render node's maker (Intel, AMD), else "" (none).
func (m Machine) VideoDevice() string {
	if len(m.GPU.NVIDIA) > 0 {
		return m.GPU.NVIDIA[0].Name
	}
	for _, r := range m.GPU.Render {
		switch r.Driver {
		case "i915", "xe":
			return "Intel graphics"
		case "amdgpu", "radeon":
			return "AMD graphics"
		case "":
			return "a render device"
		default:
			return r.Driver
		}
	}
	return ""
}

// GB is bytes in GB for people: one decimal under 10 GB (a "4 GB" NAS shows 3.7), whole
// numbers above.
func GB(bytes uint64) float64 {
	g := float64(bytes) / float64(GiB)
	if g < 10 {
		return math.Round(g*10) / 10
	}
	return math.Round(g)
}

// Reason is why a tier was chosen: the memory limit in GB and the video device ("":
// none), as Settings → This machine says it ("Standard: 8 GB, no GPU for video").
type Reason struct {
	Tier     Tier    `json:"tier"`
	MemoryGB float64 `json:"memory_gb"`
	GPU      string  `json:"gpu"`
}

// ReasonFor is m's tier reason under t.
func ReasonFor(t Tier, m Machine) Reason {
	return Reason{Tier: t, MemoryGB: GB(m.Memory.LimitBytes), GPU: m.VideoDevice()}
}

// String is the reason in English.
func (r Reason) String() string {
	gpu := r.GPU
	if gpu == "" {
		gpu = "no GPU for video"
	}
	name := string(r.Tier)
	if name != "" {
		name = strings.ToUpper(name[:1]) + name[1:]
	}
	return name + ": " + trimFloat(r.MemoryGB) + " GB, " + gpu
}

func trimFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
