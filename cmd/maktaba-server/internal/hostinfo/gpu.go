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
	// ComputeCap is its CUDA compute capability ("7.5"), Driver the driver's
	// version ("580.65.06"): "" where nvidia-smi does not say (an older driver).
	ComputeCap string `json:"compute_cap,omitempty"`
	Driver     string `json:"driver,omitempty"`
}

// The fields asked of nvidia-smi: all of them, else (a driver whose nvidia-smi
// has no compute_cap) the name and the memory.
const (
	smiFields      = "name,memory.total,compute_cap,driver_version"
	smiFieldsOlder = "name,memory.total"
)

// nvidiaSMI runs the query for fields (tests replace it).
var nvidiaSMI = func(ctx context.Context, fields string) ([]byte, error) {
	bin := smiPath()
	if bin == "" {
		return nil, exec.ErrNotFound
	}
	cmd := exec.CommandContext(ctx, bin, "--query-gpu="+fields, "--format=csv,noheader,nounits")
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
	out, err := nvidiaSMI(ctx, smiFields)
	if err != nil {
		if out, err = nvidiaSMI(ctx, smiFieldsOlder); err != nil {
			return nil
		}
	}
	return parseNVIDIA(out)
}

// parseNVIDIA reads `name, memory.total[, compute_cap, driver_version]` lines
// (MiB, no header, no units); a value nvidia-smi has not ("[N/A]") is "".
func parseNVIDIA(out []byte) []GPU {
	var gpus []GPU
	for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 2 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
			if strings.HasPrefix(f[i], "[") { // [N/A], [Not Supported]
				f[i] = ""
			}
		}
		mb, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil || f[0] == "" {
			continue
		}
		g := GPU{Name: f[0], MemoryMB: mb}
		if len(f) >= 4 {
			g.ComputeCap, g.Driver = f[2], f[3]
		}
		gpus = append(gpus, g)
	}
	return gpus
}
