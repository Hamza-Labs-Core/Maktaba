package hostinfo

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GPU is an NVIDIA GPU as nvidia-smi reports it.
type GPU struct {
	Name     string `json:"name"`
	MemoryMB uint64 `json:"memory_mb"`
}

// nvidiaSMI runs the query (tests replace it).
var nvidiaSMI = func(ctx context.Context) ([]byte, error) {
	bin := smiPath()
	if bin == "" {
		return nil, exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, bin, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	return cmd.Output()
}

// smiPath is nvidia-smi: on PATH, else (Windows) where the driver puts it.
func smiPath() string {
	if p, err := exec.LookPath("nvidia-smi"); err == nil {
		return p
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("SystemRoot"), "System32", "nvidia-smi.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "NVIDIA Corporation", "NVSMI", "nvidia-smi.exe"),
	} {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

// NVIDIA lists this machine's NVIDIA GPUs (nvidia-smi, which comes with the
// driver): none on a Mac, without the driver, or when it does not answer
// within a few seconds.
func NVIDIA() []GPU {
	if runtime.GOOS == "darwin" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := nvidiaSMI(ctx)
	if err != nil {
		return nil
	}
	return parseNVIDIA(out)
}

// parseNVIDIA reads `name, memory.total` lines (MiB, no header, no units).
func parseNVIDIA(out []byte) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		name, mem, ok := strings.Cut(strings.TrimSpace(line), ",")
		if !ok {
			continue
		}
		mb, err := strconv.ParseUint(strings.TrimSpace(mem), 10, 64)
		if err != nil || strings.TrimSpace(name) == "" {
			continue
		}
		gpus = append(gpus, GPU{Name: strings.TrimSpace(name), MemoryMB: mb})
	}
	return gpus
}
