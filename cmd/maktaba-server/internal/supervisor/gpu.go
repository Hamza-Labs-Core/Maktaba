package supervisor

import (
	"bytes"
	"context"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPU is the NVIDIA GPU a CTranslate2 transcription worker runs on (story 38.8):
// the first one nvidia-smi lists, which is CUDA device 0, the one the pipeline
// uses unless [transcription] device names another.
type GPU struct {
	Name      string
	MemoryMiB int
}

// MemoryGB is the GPU's memory in GiB.
func (g GPU) MemoryGB() float64 { return float64(g.MemoryMiB) / 1024 }

// nvidiaSMIQuery is what PlanSTT asks nvidia-smi: one "name, MiB" line per GPU.
var nvidiaSMIQuery = []string{"--query-gpu=name,memory.total", "--format=csv,noheader,nounits"}

// nvidiaSMITimeout bounds the query: a wedged driver can leave nvidia-smi hanging,
// and the supervisor must start anyway (on the CPU plan).
const nvidiaSMITimeout = 10 * time.Second

// nvidiaSMI runs the query and returns what it printed; tests replace it. No
// nvidia-smi (no NVIDIA driver) is an error like any other: no GPU.
var nvidiaSMI = func() ([]byte, error) {
	bin, err := nvidiaSMIPath()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), nvidiaSMITimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, nvidiaSMIQuery...)
	hideWindow(cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// nvidiaSMIPath finds nvidia-smi on PATH, else where the driver installs it
// (nvidiaSMIDirs: a service's PATH may not have it).
func nvidiaSMIPath() (string, error) {
	bin, err := exec.LookPath("nvidia-smi")
	if err == nil {
		return bin, nil
	}
	for _, p := range nvidiaSMIDirs() {
		if found, lerr := exec.LookPath(p); lerr == nil {
			return found, nil
		}
	}
	return "", err
}

// parseNvidiaSMI reads the query's first line ("NVIDIA GeForce RTX 2080 SUPER,
// 8192"); false when there is no GPU in it.
func parseNvidiaSMI(out []byte) (GPU, bool) {
	for _, line := range strings.Split(string(out), "\n") {
		name, mem, ok := strings.Cut(strings.TrimSpace(line), ",")
		if !ok {
			continue
		}
		mib, err := strconv.Atoi(strings.TrimSpace(mem))
		if err != nil || mib <= 0 {
			continue
		}
		return GPU{Name: strings.TrimSpace(name), MemoryMiB: mib}, true
	}
	return GPU{}, false
}

// gpuProbe holds the answer: nvidia-smi is asked once per process (PlanSTT runs
// for every pipeline process the supervisor builds).
var gpuProbe struct {
	sync.Mutex
	done bool
	gpu  GPU
	ok   bool
}

// NvidiaGPU is this machine's NVIDIA GPU, as nvidia-smi reports it; false when it
// has none (or no driver).
func NvidiaGPU() (GPU, bool) {
	gpuProbe.Lock()
	defer gpuProbe.Unlock()
	if !gpuProbe.done {
		gpuProbe.done, gpuProbe.gpu, gpuProbe.ok = true, GPU{}, false
		if out, err := nvidiaSMI(); err == nil {
			gpuProbe.gpu, gpuProbe.ok = parseNvidiaSMI(out)
		}
	}
	return gpuProbe.gpu, gpuProbe.ok
}

// forgetGPU makes the next NvidiaGPU ask again (tests).
func forgetGPU() {
	gpuProbe.Lock()
	gpuProbe.done = false
	gpuProbe.Unlock()
}
