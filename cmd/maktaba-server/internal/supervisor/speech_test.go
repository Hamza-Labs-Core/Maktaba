package supervisor

import (
	"slices"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// Epic 59 story 59.06: the tier's speech, and what server.toml changes of it.
func TestSpeechOf(t *testing.T) {
	fakeMachine(t, false, 8)
	avx2 := hostinfo.Machine{Arch: "amd64", CPU: hostinfo.CPU{AVX2: true}}
	j4125 := hostinfo.Machine{Arch: "amd64"}
	arm := hostinfo.Machine{Arch: "arm64"}
	cfg := config.Defaults()
	at := func(tier, speech, model string, m hostinfo.Machine) Speech {
		c := cfg
		c.Server.Tier, c.Transcription.Speech, c.Transcription.Model = tier, speech, model
		return SpeechOf(c, m)
	}
	if s := at("light", "", "", arm); s.On || s.OnSet || s.Model != "base" || s.Reason != hostinfo.SpeechLight ||
		!s.QuietOnly || !s.Polite || s.SecondListen || !slices.Equal(s.Offered, []string{"tiny", "base"}) ||
		s.Repo != "Systran/faster-whisper-base" {
		t.Fatalf("light arm64: %+v", s)
	}
	// the one tap: on, base
	if s := at("light", "on", "base", arm); !s.On || !s.OnSet || !s.ModelSet || s.Repo != "Systran/faster-whisper-base" {
		t.Fatalf("light turned on: %+v", s)
	}
	if s := at("standard", "", "", j4125); !s.On || s.Model != "base" || s.Reason != hostinfo.SpeechNoAVX2 || s.QuietOnly {
		t.Fatalf("standard J4125: %+v", s)
	}
	if s := at("standard", "", "", avx2); s.Model != "small" || s.Repo != "Systran/faster-whisper-small" || !s.SecondListen {
		t.Fatalf("standard avx2: %+v", s)
	}
	if s := at("strong", "", "", avx2); s.Model != "large-v3-turbo" || s.Polite || s.Repo != "dropbox-dash/faster-whisper-large-v3-turbo" {
		t.Fatalf("strong: %+v", s)
	}
	// an operator's repo stands; an old short name ("large-v3", setup's) is the tier's
	if s := at("standard", "", "Systran/faster-whisper-medium", avx2); s.Model != "medium" || s.Repo != "Systran/faster-whisper-medium" {
		t.Fatalf("a repo: %+v", s)
	}
	if s := at("standard", "off", "large-v3", avx2); s.On || s.ModelSet || s.Model != "small" {
		t.Fatalf("off, an old name: %+v", s)
	}
	// no tier read: today's (on, the pipeline's own default model)
	if s := at("", "", "", avx2); !s.On || s.Repo != "" {
		t.Fatalf("no tier: %+v", s)
	}
	appleSilicon = func() bool { return true }
	if s := at("standard", "", "", hostinfo.Machine{Arch: "arm64"}); s.Engine != "mlx" || s.Model != "large-v3-turbo" || s.Repo != "" || !s.SecondListen {
		t.Fatalf("apple silicon: %+v", s)
	}
}

// Speech off (Light until turned on): no worker transcribes, the main worker leaves the
// stages; on, Light and Standard run at nice 10 with a thread per two cores.
func TestPlanSTTBySpeech(t *testing.T) {
	fakeMachine(t, false, 4)
	noSTTEnv(t)
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "")
	speechMachine = func() hostinfo.Machine { return hostinfo.Machine{Arch: "arm64"} }
	cfg := config.Defaults()
	cfg.Server.Tier = "light"
	p := PlanSTT(cfg)
	if !p.SpeechOff || p.Workers != 0 {
		t.Fatalf("light: %+v", p)
	}
	env := strings.Join(p.mainEnv(nil), " ")
	if !strings.Contains(env, "MAKTABA_PIPELINE_EXCLUDE_STAGES=transcribe,second_listen") {
		t.Fatalf("light's main worker: %s", env)
	}
	cfg.Transcription.Speech = "on"
	p = PlanSTT(cfg)
	if p.SpeechOff || p.Nice != SpeechNice || p.CPUThreads != 4 {
		t.Fatalf("light turned on: %+v", p)
	}
	env = strings.Join(p.mainEnv(nil), " ")
	if strings.Contains(env, "EXCLUDE_STAGES") || !strings.Contains(env, "MAKTABA_STT_NICE=10") || !strings.Contains(env, "MAKTABA_STT_CPU_THREADS=4") {
		t.Fatalf("light on, the main worker: %s", env)
	}
	if got := sttEnv(cfg); !slices.Contains(got, "MAKTABA_STT_MODEL=Systran/faster-whisper-base") {
		t.Fatalf("light's model: %v", got)
	}
	cfg.Server.Tier = "strong"
	if p := PlanSTT(cfg); p.Nice != 0 || p.CPUThreads != 4 {
		t.Fatalf("strong keeps today's: %+v", p)
	}
}

// Story 59.06 T04: with a 6 GB limit two workers of small do not fit: one starts, and the
// plan says why for the log.
func TestPlanSTTCapsSpeechWorkersByMemory(t *testing.T) {
	fakeMachine(t, false, 6)
	noSTTEnv(t)
	t.Setenv("MAKTABA_PIPELINE_EXCLUDE_STAGES", "")
	t.Setenv("MAKTABA_PIPELINE_STAGES", "")
	cfg := config.Defaults()
	cfg.Server.Tier = "standard"
	cfg.Transcription.Workers = 2
	p := PlanSTT(cfg)
	if p.Workers != 1 || !strings.Contains(p.Capped, "2 speech workers of small") || !strings.Contains(p.Capped, "1 start") {
		t.Fatalf("6 GB: %+v", p)
	}
	totalMemory = func() uint64 { return 16 << 30 }
	if p := PlanSTT(cfg); p.Workers != 2 || p.Capped != "" {
		t.Fatalf("16 GB: %+v", p)
	}
}
