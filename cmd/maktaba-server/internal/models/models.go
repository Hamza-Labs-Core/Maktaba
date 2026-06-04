// Package models manages the Whisper transcription models the pipeline
// uses. It keeps a small catalog of the supported model identifiers,
// detects which are already installed under the data dir, and downloads
// new ones.
//
// The model formats Maktaba supports (MLX for Apple Silicon,
// faster-whisper/CTranslate2 for CUDA/CPU) are multi-file HuggingFace
// repositories, so the actual fetch delegates to the HuggingFace CLI
// (`hf download` / `huggingface-cli download`) when present — that is
// the maintained, resumable path for these repos. The catalog and
// install-detection logic is kept independent of the downloader so it
// stays unit-testable without network or external tools.
package models

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// Model is a catalog entry: a stable short name the user types, the
// HuggingFace repo it resolves to, the backend it targets, and a hint
// about the hardware it suits.
type Model struct {
	Name    string // short name, e.g. "mlx-whisper-large-v3"
	Repo    string // HuggingFace repo id
	Backend string // "mlx-whisper" | "faster-whisper"
	Note    string // human hint
}

// Catalog is the supported set. Kept deliberately small — these are the
// two the spec names plus their smaller siblings for low-RAM hosts.
func Catalog() []Model {
	return []Model{
		{"mlx-whisper-large-v3", "mlx-community/whisper-large-v3-mlx", "mlx-whisper", "Apple Silicon (MLX), best quality"},
		{"mlx-whisper-medium", "mlx-community/whisper-medium-mlx", "mlx-whisper", "Apple Silicon (MLX), lower RAM"},
		{"faster-whisper-large-v3", "Systran/faster-whisper-large-v3", "faster-whisper", "CUDA/CPU (CTranslate2), best quality"},
		{"faster-whisper-medium", "Systran/faster-whisper-medium", "faster-whisper", "CUDA/CPU (CTranslate2), lower RAM"},
	}
}

// Lookup finds a catalog entry by name.
func Lookup(name string) (Model, bool) {
	for _, m := range Catalog() {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// Dir is the models directory under the data dir.
func Dir(dataDir string) string { return filepath.Join(dataDir, "models") }

// Installed reports whether a model's directory exists and is non-empty
// under the data dir.
func Installed(dataDir, name string) bool {
	entries, err := os.ReadDir(filepath.Join(Dir(dataDir), name))
	return err == nil && len(entries) > 0
}

// ListInstalled returns the names of models present on disk, sorted.
func ListInstalled(dataDir string) []string {
	entries, err := os.ReadDir(Dir(dataDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && Installed(dataDir, e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// Download fetches a catalog model into the data dir using the
// HuggingFace CLI. It returns an actionable error if neither `hf` nor
// `huggingface-cli` is on PATH, since these multi-file repos need a
// resumable downloader rather than a hand-rolled HTTP loop.
func Download(dataDir, name string) error {
	m, ok := Lookup(name)
	if !ok {
		return fmt.Errorf("unknown model %q (run `maktaba-server models list`)", name)
	}
	dest := filepath.Join(Dir(dataDir), name)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create model dir: %w", err)
	}

	cli, args := hfCommand(m.Repo, dest)
	if cli == "" {
		return fmt.Errorf(
			"no HuggingFace CLI found on PATH (install with `pip install -U \"huggingface_hub[cli]\"`), "+
				"then re-run; or fetch %s into %s manually", m.Repo, dest)
	}

	cmd := exec.Command(cli, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download %s via %s: %w", m.Repo, cli, err)
	}
	return nil
}

// hfCommand picks the available HuggingFace CLI and builds its download
// argv. Returns ("", nil) when none is on PATH. The newer `hf` binary is
// preferred; `huggingface-cli` is the legacy fallback (same semantics).
func hfCommand(repo, dest string) (string, []string) {
	if p, err := exec.LookPath("hf"); err == nil {
		return p, []string{"download", repo, "--local-dir", dest}
	}
	if p, err := exec.LookPath("huggingface-cli"); err == nil {
		return p, []string{"download", repo, "--local-dir", dest}
	}
	return "", nil
}
