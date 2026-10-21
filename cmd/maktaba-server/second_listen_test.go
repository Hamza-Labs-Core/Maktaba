package main

import (
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/hostinfo"
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
		{"one GPU of two can", "windows", "amd64", []hostinfo.GPU{rtx2080, rtx3080}, true, ""},
		{"none of two can: the largest one's reason", "windows", "amd64", []hostinfo.GPU{rtx2080, gtx1080}, false, "second_listen_gpu_old"},
	} {
		got := secondListenOn(c.goos, c.arch, c.gpus)
		if got.Runs != c.runs || got.Code != c.code {
			t.Errorf("%s: %+v, want runs %v code %q", c.name, got, c.runs, c.code)
		}
		if !got.Runs && got.Message == "" {
			t.Errorf("%s: no English", c.name)
		}
	}
	small := secondListenOn("windows", "amd64", []hostinfo.GPU{rtx2080})
	if small.Params["have_bytes"] != uint64(8192)<<20 || small.Params["need_bytes"] != uint64(secondListenMinMiBWindows)<<20 ||
		small.Params["gpu"] != rtx2080.Name {
		t.Errorf("the small GPU's values: %v", small.Params)
	}
}
