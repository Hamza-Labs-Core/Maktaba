package supervisor

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GPU is the NVIDIA GPU a CTranslate2 transcription worker runs on (story 38.8):
// the first one nvidia-smi lists, which is CUDA device 0 (the pipeline numbers them
// as nvidia-smi does), the one the pipeline uses unless [transcription] device names
// another. Story 38.9: its compute capability and driver, which the second listen
// needs (0 and "": an older nvidia-smi that does not say).
type GPU struct {
	Name         string
	MemoryMiB    int
	ComputeMajor int
	ComputeMinor int
	Driver       string
}

// DriverMajor is the driver's branch (580 of "580.88"); 0 when unknown.
func (g GPU) DriverMajor() int {
	head, _, _ := strings.Cut(g.Driver, ".")
	n, err := strconv.Atoi(strings.TrimSpace(head))
	if err != nil {
		return 0
	}
	return n
}

// MemoryGB is the GPU's memory in GiB.
func (g GPU) MemoryGB() float64 { return float64(g.MemoryMiB) / 1024 }

// nvidiaSMIQuery is what PlanSTT asks nvidia-smi: one "name, MiB, compute
// capability, driver" line per GPU; nvidiaSMIFallback what it asks an nvidia-smi too
// old for compute_cap (before R510), which knows the driver_version field.
var (
	nvidiaSMIQuery    = []string{"--query-gpu=name,memory.total,compute_cap,driver_version", "--format=csv,noheader,nounits"}
	nvidiaSMIFallback = []string{"--query-gpu=name,memory.total,driver_version", "--format=csv,noheader,nounits"}
)

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
	ask := func(query []string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), nvidiaSMITimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, query...)
		hideWindow(cmd)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		if ctx.Err() != nil {
			return nil, ctx.Err() // wedged: no second try
		}
		return out.Bytes(), err
	}
	out, err := ask(nvidiaSMIQuery)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		out, err = ask(nvidiaSMIFallback)
	}
	if err != nil {
		return nil, err
	}
	return out, nil
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
// 8192, 7.5, 616.64", or the fallback's "name, MiB, driver", or just "name, MiB");
// false when there is no GPU in it. A field it cannot read stays unknown.
func parseNvidiaSMI(out []byte) (GPU, bool) {
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimSpace(line), ",")
		if len(f) < 2 {
			continue
		}
		for i := range f {
			f[i] = strings.TrimSpace(f[i])
		}
		mib, err := strconv.Atoi(f[1])
		if err != nil || mib <= 0 {
			continue
		}
		g := GPU{Name: f[0], MemoryMiB: mib}
		driver := ""
		switch len(f) {
		case 3:
			driver = f[2]
		case 4:
			if major, minor, ok := strings.Cut(f[2], "."); ok {
				a, errA := strconv.Atoi(major)
				b, errB := strconv.Atoi(minor)
				if errA == nil && errB == nil {
					g.ComputeMajor, g.ComputeMinor = a, b
				}
			}
			driver = f[3]
		}
		if driver != "" && driver != "[N/A]" {
			g.Driver = driver
		}
		return g, true
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
