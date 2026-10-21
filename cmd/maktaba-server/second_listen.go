package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
)

// What the second listen needs on Windows and Linux (Cohere Transcribe on torch
// CUDA, beside Whisper on the same GPU; Epic 38 story 38.9). The pipeline's room
// check and the supervisor's tiers use the same values (SecondListenFits).
const (
	// secondListenMinMiBWindows: Windows keeps part of a GPU's memory for the
	// desktop, and a process that runs out spills into system memory (several
	// times slower) instead of failing
	secondListenMinMiBWindows = 9728 // 9.5 GiB
	secondListenMinMiB        = 7680 // 7.5 GiB
	// torch's CUDA 13 build: Turing (compute capability 7.5) or newer, and an
	// R580 driver or newer
	secondListenMinCompute = 7.5
	secondListenMinDriver  = 580
)

// secondListen is whether this machine can run the second listen, and if not,
// why (a code, its values and its English; setup.check.note.<code>).
type secondListen struct {
	Runs    bool           `json:"runs"`
	Code    string         `json:"code,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
	Message string         `json:"message,omitempty"`
}

// secondListenOn: on Apple Silicon it runs on MLX (Epic 36); on Windows and Linux
// on an NVIDIA GPU that has the memory beside Whisper, a compute capability of
// 7.5 or more and an R580 driver or newer. A value nvidia-smi does not report is
// not held against the GPU. Of several GPUs, one that can is enough; when none
// can, the reason is the largest one's.
func secondListenOn(goos, arch string, gpus []hostinfo.GPU) secondListen {
	if goos == "darwin" {
		if arch == "arm64" {
			return secondListen{Runs: true}
		}
		return secondListen{Code: "second_listen_intel_mac",
			Message: "This Mac has an Intel processor: the second listen runs on Apple Silicon."}
	}
	if len(gpus) == 0 {
		return secondListen{Code: "second_listen_no_gpu",
			Message: "No NVIDIA GPU: the second listen needs one, so Whisper's words are kept."}
	}
	need := uint64(secondListenMinMiB)
	if goos == "windows" {
		need = secondListenMinMiBWindows
	}
	var largest *secondListen
	var largestMiB uint64
	for _, g := range gpus {
		why := secondListenGPU(g, need)
		if why == nil {
			return secondListen{Runs: true}
		}
		if largest == nil || g.MemoryMB > largestMiB {
			largest, largestMiB = why, g.MemoryMB
		}
	}
	return *largest
}

// secondListenGPU is why g cannot run the second listen, or nil.
func secondListenGPU(g hostinfo.GPU, needMiB uint64) *secondListen {
	if c, err := strconv.ParseFloat(g.ComputeCap, 64); err == nil && c < secondListenMinCompute {
		return &secondListen{Code: "second_listen_gpu_old",
			Params:  map[string]any{"gpu": g.Name, "compute": g.ComputeCap},
			Message: fmt.Sprintf("%s is older than the second listen supports (compute capability %s: it needs Turing, 7.5, or newer).", g.Name, g.ComputeCap)}
	}
	if major, ok := driverMajor(g.Driver); ok && major < secondListenMinDriver {
		return &secondListen{Code: "second_listen_driver_old",
			Params:  map[string]any{"gpu": g.Name, "driver": g.Driver, "need": strconv.Itoa(secondListenMinDriver)},
			Message: fmt.Sprintf("The NVIDIA driver %s is older than the second listen needs (%d or newer): update the driver.", g.Driver, secondListenMinDriver)}
	}
	if g.MemoryMB < needMiB {
		return &secondListen{Code: "second_listen_gpu_small",
			Params: map[string]any{"gpu": g.Name, "have_bytes": g.MemoryMB << 20, "need_bytes": needMiB << 20},
			Message: fmt.Sprintf("%s has %d MiB: the second listen needs a GPU of %d MiB or more beside Whisper here, so Whisper's words are kept.",
				g.Name, g.MemoryMB, needMiB)}
	}
	return nil
}

// driverMajor is a driver version's first number ("580.65.06": 580).
func driverMajor(v string) (int, bool) {
	major, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	n, err := strconv.Atoi(major)
	return n, err == nil
}
