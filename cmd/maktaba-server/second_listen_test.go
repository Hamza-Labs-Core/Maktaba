package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/supervisor"
)

func TestSecondListenOn(t *testing.T) {
	gpu := func(name string, mib uint64, compute, driver string) hostinfo.GPU {
		return hostinfo.GPU{Name: name, MemoryMB: mib, ComputeCap: compute, Driver: driver}
	}
	rtx2080 := gpu("NVIDIA GeForce RTX 2080 SUPER", 8192, "7.5", "581.57") // this PC
	rtx3080 := gpu("NVIDIA GeForce RTX 3080", 10240, "8.6", "580.82")
	gtx1080 := gpu("NVIDIA GeForce GTX 1080 Ti", 11264, "6.1", "581.57")
	oldDriver := gpu("NVIDIA RTX A4000", 16376, "8.6", "552.22")
	unknown := gpu("NVIDIA RTX A4000", 16376, "", "") // an older nvidia-smi: not held against it
	for _, c := range []struct {
		name       string
		goos, arch string
		gpus       []hostinfo.GPU
		runs       bool
		code       string
	}{
		{"Apple Silicon", "darwin", "arm64", nil, true, ""},
		{"an Intel Mac", "darwin", "amd64", nil, false, "second_listen_intel_mac"},
		{"no GPU", "windows", "amd64", nil, false, "second_listen_no_gpu"},
		{"8 GB on Windows", "windows", "amd64", []hostinfo.GPU{rtx2080}, false, "second_listen_gpu_small"},
		{"8 GB on Linux", "linux", "amd64", []hostinfo.GPU{rtx2080}, true, ""},
		{"10 GB on Windows", "windows", "amd64", []hostinfo.GPU{rtx3080}, true, ""},
		{"a pre-Turing GPU", "windows", "amd64", []hostinfo.GPU{gtx1080}, false, "second_listen_gpu_old"},
		{"a driver before R580", "linux", "amd64", []hostinfo.GPU{oldDriver}, false, "second_listen_driver_old"},
		{"no compute capability or driver said", "windows", "amd64", []hostinfo.GPU{unknown}, true, ""},
		// the GPU transcription uses decides: the first, as the supervisor's and the pipeline's
		{"the first of two can", "windows", "amd64", []hostinfo.GPU{rtx3080, rtx2080}, true, ""},
		{"only the second of two can", "windows", "amd64", []hostinfo.GPU{rtx2080, rtx3080}, false, "second_listen_gpu_small"},
		{"Linux on arm64", "linux", "arm64", []hostinfo.GPU{rtx3080}, false, "second_listen_arch"},
		{"Windows on arm64", "windows", "arm64", nil, false, "second_listen_arch"},
	} {
		got := secondListenOn(c.goos, c.arch, c.gpus)
		if got.Runs != c.runs || got.Code != c.code {
			t.Errorf("%s: %+v, want runs %v code %q", c.name, got, c.runs, c.code)
		}
		if !got.Runs && got.Message == "" {
			t.Errorf("%s: no English", c.name)
		}
	}
	if arm := secondListenOn("linux", "arm64", nil); arm.Params["arch"] != "arm64" {
		t.Errorf("the arch's value: %v", arm.Params)
	}
	small := secondListenOn("windows", "amd64", []hostinfo.GPU{rtx2080})
	if small.Params["have_bytes"] != uint64(8192)<<20 || small.Params["need_bytes"] != uint64(secondListenMinMiBWindows)<<20 ||
		small.Params["gpu"] != rtx2080.Name {
		t.Errorf("the small GPU's values: %v", small.Params)
	}
}

// Review of #209 (round 4): setup asks for the Hugging Face sign-in where the
// supervisor plans the second listen, and nowhere else: the same GPU (the first) and
// the same thresholds, on this machine's OS.
func TestSetupAndTheSupervisorAgreeOnTheGPU(t *testing.T) {
	gpu := func(mib uint64, compute, driver string) hostinfo.GPU {
		return hostinfo.GPU{Name: "NVIDIA test", MemoryMB: mib, ComputeCap: compute, Driver: driver}
	}
	// as the supervisor reads nvidia-smi's line for it
	theirs := func(g hostinfo.GPU) supervisor.GPU {
		s := supervisor.GPU{Name: g.Name, MemoryMiB: int(g.MemoryMB), Driver: g.Driver}
		if major, minor, ok := strings.Cut(g.ComputeCap, "."); ok {
			s.ComputeMajor, _ = strconv.Atoi(major)
			s.ComputeMinor, _ = strconv.Atoi(minor)
		}
		return s
	}
	var cases [][]hostinfo.GPU
	for _, mib := range []uint64{4096, 7679, 7680, 8192, 9727, 9728, 12288, 24564} {
		for _, compute := range []string{"", "6.1", "7.0", "7.5", "8.6"} {
			for _, driver := range []string{"", "552.22", "579.99", "580.65.06", "591.44"} {
				cases = append(cases, []hostinfo.GPU{gpu(mib, compute, driver)})
			}
		}
	}
	big, small := gpu(24564, "8.9", "591.44"), gpu(4096, "8.6", "591.44")
	cases = append(cases, []hostinfo.GPU{big, small}, []hostinfo.GPU{small, big})
	for _, gpus := range cases {
		setup := secondListenOn(runtime.GOOS, "amd64", gpus).Runs
		if runtime.GOOS == "darwin" {
			// a Mac has no CUDA: setup never asks, and the supervisor never sees an
			// NVIDIA GPU there to plan on (no nvidia-smi)
			if setup {
				t.Errorf("%+v on darwin: setup asks for the sign-in", gpus)
			}
			continue
		}
		plans := supervisor.SecondListenGPU(theirs(gpus[0])) == ""
		if setup != plans {
			t.Errorf("%+v on %s: setup says %v, the supervisor %v", gpus, runtime.GOOS, setup, plans)
		}
	}
}

// Every reason setup gives for no second listen has its words in both of the web's
// languages (setup.check.note.<code>).
func TestEverySecondListenReasonHasItsWords(t *testing.T) {
	src, err := os.ReadFile("second_listen.go")
	if err != nil {
		t.Fatal(err)
	}
	codes := regexp.MustCompile(`Code:\s*"([a-z_]+)"`).FindAllStringSubmatch(string(src), -1)
	if len(codes) < 6 {
		t.Fatalf("found %d codes: the pattern no longer finds them", len(codes))
	}
	for _, lang := range []string{"en", "ar"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "i18n", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var words map[string]string
		if err := json.Unmarshal(raw, &words); err != nil {
			t.Fatal(err)
		}
		for _, c := range codes {
			if strings.TrimSpace(words["setup.check.note."+c[1]]) == "" {
				t.Errorf("%s.json has no setup.check.note.%s", lang, c[1])
			}
		}
	}
}
