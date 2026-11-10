package supervisor

// Speech to text by hardware tier (Epic 59 story 59.06). The tier (59.04) sets the default:
// Light off until turned on, then base, in the quiet hours only; Standard small (base without
// AVX2 or on ARM); Strong large-v3-turbo with AVX2 or CUDA. [transcription] speech and model
// in server.toml override it, and on Apple Silicon MLX keeps large-v3-turbo on the GPU. On
// the Light and Standard tiers a transcribing process runs at nice 10 with one CTranslate2
// thread per two cores, and no more speech workers start than a quarter of the memory limit
// holds (the model plus 300 MB each).

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// SpeechNice is a transcribing process's niceness on the Light and Standard tiers.
const SpeechNice = 10

// Speech is this machine's speech to text, as server.toml and the tier make it.
type Speech struct {
	// On: speech to text runs here; OnSet: [transcription] speech says so (else the tier).
	On    bool `json:"on"`
	OnSet bool `json:"on_set"`
	// Model is the size it runs ("base"), or the operator's own repo or folder; ModelSet:
	// [transcription] model names it (else the tier's).
	Model    string `json:"model"`
	ModelSet bool   `json:"model_set"`
	// Repo is what the pipeline loads (MAKTABA_STT_MODEL; "": the pipeline's own default).
	Repo string `json:"repo"`
	// Default, DefaultOn and Reason are the tier's (hostinfo.Defaults).
	Default   string `json:"default"`
	DefaultOn bool   `json:"default_on"`
	Reason    string `json:"reason,omitempty"`
	// Engine: "ct2" (Windows and Linux) or "mlx" (Apple Silicon, which keeps its model).
	Engine string `json:"engine"`
	// Offered are the sizes Settings offers here, fastest first.
	Offered []string `json:"offered"`
	// QuietOnly: transcription waits for the quiet hours, a single new file's too (Light).
	QuietOnly bool `json:"quiet_only"`
	// Polite: nice 10 and one thread per two cores (Light and Standard).
	Polite bool `json:"polite"`
	// SecondListen: the second listen can run on this kind of machine at all (not on ARM
	// Linux: torch is x86_64 + CUDA there).
	SecondListen bool   `json:"second_listen"`
	Arch         string `json:"arch"`
}

// speechMachine is the hardware the speech defaults are read from, once (tests replace it).
var speechMachine = sync.OnceValue(hostinfo.Detect)

// SpeechOf is cfg's speech on machine m.
func SpeechOf(cfg config.Config, m hostinfo.Machine) Speech {
	t := cfg.Transcription
	tier, tiered := hostinfo.ParseTier(cfg.Server.Tier)
	d := hostinfo.DefaultsFor(tier, m)
	if !tiered { // no tier read (a single role, an old file): today's, speech on
		d = hostinfo.Defaults{SpeechOn: true, SpeechModel: hostinfo.ModelTurbo}
	}
	s := Speech{Engine: "ct2", Default: d.SpeechModel, DefaultOn: d.SpeechOn, Reason: d.SpeechReason,
		QuietOnly: d.SpeechInQuietHours, Arch: m.Arch,
		Polite:       tier == hostinfo.TierLight || tier == hostinfo.TierStandard,
		SecondListen: m.Arch != "arm64" || appleSilicon()}
	if appleSilicon() {
		s.Engine, s.Default, s.Reason, s.Polite = "mlx", hostinfo.ModelTurbo, "", false
	}
	s.Offered = hostinfo.SpeechOffered(tier, s.Default)
	if !tiered {
		s.Offered = hostinfo.SpeechOffered(hostinfo.TierStrong, s.Default)
	}
	switch strings.ToLower(strings.TrimSpace(t.Speech)) {
	case "on", "true", "yes", "1":
		s.On, s.OnSet = true, true
	case "off", "false", "no", "0":
		s.On, s.OnSet = false, true
	default:
		s.On = d.SpeechOn
	}
	model := strings.TrimSpace(t.Model)
	switch {
	case strings.Contains(model, "/") || strings.Contains(model, `\`):
		// a repo or a folder of the operator's own: as today
		s.Model, s.ModelSet, s.Repo = model, true, model
		if sm, ok := hostinfo.SpeechModelOfRepo(model); ok {
			s.Model = sm.Name
		}
	case speechSize(model):
		s.Model, s.ModelSet = model, true
	default:
		// empty, or an old short name setup wrote ("large-v3"): the tier's
		s.Model = s.Default
	}
	if s.Engine == "ct2" && s.Repo == "" && (s.ModelSet || tiered) {
		if sm, ok := hostinfo.SpeechModelNamed(s.Model); ok {
			s.Repo = sm.Repo
		}
	}
	if s.Engine == "mlx" && !strings.Contains(model, "/") {
		s.Model, s.Repo = hostinfo.ModelTurbo, "" // MLX's own conversion, as before
	}
	return s
}

// speechSize reports whether name is one of hostinfo.SpeechModels.
func speechSize(name string) bool {
	_, ok := hostinfo.SpeechModelNamed(name)
	return ok
}

// speechEnv is what every pipeline process gets of the speech settings: the model the
// tier or server.toml picks (MAKTABA_STT_MODEL) on the CTranslate2 engine.
func speechEnv(s Speech) []string {
	if s.Repo == "" {
		return nil
	}
	return []string{"MAKTABA_STT_MODEL=" + s.Repo}
}

// SpeechMeasureCmd is the speech measure (python -m maktaba_pipeline.stt.benchmark) as this
// machine's transcribing worker would run: its Python and environment, the model speech
// runs with, its threads and niceness; the result is written to out.
func SpeechMeasureCmd(ctx context.Context, opts Options, out string) (*exec.Cmd, error) {
	base, err := ChildEnv(RolePipeline, opts.Config)
	if err != nil {
		return nil, err
	}
	env := append(os.Environ(), base...)
	env = append(env, PlanFor(opts).knobEnv()...)
	cmd := exec.CommandContext(ctx, pythonFor(opts), "-m", "maktaba_pipeline.stt.benchmark", "--out", out)
	cmd.Env = env
	hideWindow(cmd)
	return cmd, nil
}

// SpeechMachine is the hardware the speech defaults are read from.
func SpeechMachine() hostinfo.Machine { return speechMachine() }
