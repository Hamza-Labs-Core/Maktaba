package hostinfo

// Speech to text by tier (Epic 59 story 59.06): the Whisper sizes a machine is offered, what
// each costs in memory and time, and why a tier picked the one it did. The pipeline knows
// the same sizes by name (stt/engine.py SIZES, kept equal by a test).

// Why a tier's speech model is the one it is (Defaults.SpeechReason): Settings says it.
const (
	// SpeechLight: the Light tier keeps speech off until it is turned on, then base.
	SpeechLight = "light"
	// SpeechCUDA: a CUDA GPU raised the model to large-v3-turbo.
	SpeechCUDA = "cuda"
	// SpeechNoAVX2: an x86 processor without AVX2 got a smaller model.
	SpeechNoAVX2 = "no_avx2"
	// SpeechARM: an ARM processor got a smaller model.
	SpeechARM = "arm"
)

// SpeechModel is a Whisper size as CTranslate2 runs it.
type SpeechModel struct {
	// Name is the size ("base"), as server.toml's [transcription] model takes it.
	Name string `json:"name"`
	// Repo is its CTranslate2 conversion on Hugging Face (the catalog's model.whisper-*).
	Repo string `json:"repo"`
	// DownloadMB is its files' size.
	DownloadMB int `json:"download_mb"`
	// WorkerMB is a speech worker's memory with it: the model plus 300 MB.
	WorkerMB int `json:"worker_mb"`
	// Cost is its CPU time per second of audio relative to base's (faster-whisper's own CPU
	// int8 numbers: turbo's 4-layer decoder makes it faster than medium): the estimate of a
	// model that was not measured is the measured one's scaled by the ratio of their costs.
	Cost float64 `json:"cost"`
}

// workerOverheadMB is what a speech worker takes beside its model.
const workerOverheadMB = 300

// SpeechModels are the sizes, fastest first.
var SpeechModels = []SpeechModel{
	{Name: "tiny", Repo: "Systran/faster-whisper-tiny", DownloadMB: 78, WorkerMB: 78 + workerOverheadMB, Cost: 0.5},
	{Name: ModelBase, Repo: "Systran/faster-whisper-base", DownloadMB: 148, WorkerMB: 148 + workerOverheadMB, Cost: 1},
	{Name: ModelSmall, Repo: "Systran/faster-whisper-small", DownloadMB: 486, WorkerMB: 486 + workerOverheadMB, Cost: 3},
	{Name: ModelTurbo, Repo: "dropbox-dash/faster-whisper-large-v3-turbo", DownloadMB: 1618, WorkerMB: 1618 + workerOverheadMB, Cost: 5},
	{Name: "medium", Repo: "Systran/faster-whisper-medium", DownloadMB: 1528, WorkerMB: 1528 + workerOverheadMB, Cost: 8},
}

// SpeechModelNamed is the size called name (false: none).
func SpeechModelNamed(name string) (SpeechModel, bool) {
	for _, s := range SpeechModels {
		if s.Name == name {
			return s, true
		}
	}
	return SpeechModel{}, false
}

// SpeechModelOfRepo is the size whose conversion is repo (false: none).
func SpeechModelOfRepo(repo string) (SpeechModel, bool) {
	for _, s := range SpeechModels {
		if s.Repo == repo {
			return s, true
		}
	}
	return SpeechModel{}, false
}

// SpeechRaisedByGPU reports whether m's CUDA GPU raises the speech model to large-v3-turbo:
// the GPU takes the work (4 GB or more: a smaller card transcribes on the CPU) and the
// memory limit holds its worker (an unknown limit does).
func SpeechRaisedByGPU(m Machine) bool {
	if m.CUDAMiB() < turboGPUMiB {
		return false
	}
	turbo, _ := SpeechModelNamed(ModelTurbo)
	return m.Memory.LimitBytes == 0 || m.Memory.LimitBytes>>20 >= uint64(turbo.WorkerMB)
}

// SpeechOffered are the sizes tier t offers, fastest first: Light tiny and base, Standard up
// to medium, Strong all; the tier's own default always.
func SpeechOffered(t Tier, def string) []string {
	var out []string
	for _, s := range SpeechModels {
		ok := true
		switch t {
		case TierLight:
			ok = s.Name == "tiny" || s.Name == ModelBase
		case TierStandard:
			ok = s.Name != ModelTurbo
		}
		if ok || s.Name == def {
			out = append(out, s.Name)
		}
	}
	return out
}

// SpeechNextSmaller is the next faster size than name among offered (false: name is the
// fastest offered, or not a size).
func SpeechNextSmaller(name string, offered []string) (string, bool) {
	cur, ok := SpeechModelNamed(name)
	if !ok {
		return "", false
	}
	best, found := SpeechModel{}, false
	for _, o := range offered {
		s, ok := SpeechModelNamed(o)
		if ok && s.Cost < cur.Cost && (!found || s.Cost > best.Cost) {
			best, found = s, true
		}
	}
	return best.Name, found
}

// SpeechWorkersFit is how many speech workers of model a memory limit of limitBytes holds:
// a quarter of the limit for them (the rest is Postgres, the server, streaming and the
// NAS's own services), at least one; 0 limit (unknown): no cap (-1).
func SpeechWorkersFit(limitBytes uint64, model string) int {
	s, ok := SpeechModelNamed(model)
	if limitBytes == 0 || !ok {
		return -1
	}
	n := int(limitBytes>>20) / 4 / s.WorkerMB
	return max(1, n)
}
