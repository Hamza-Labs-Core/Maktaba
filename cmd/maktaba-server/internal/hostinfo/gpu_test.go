package hostinfo

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
)

func TestParseNVIDIA(t *testing.T) {
	out := []byte("NVIDIA GeForce RTX 2080 SUPER, 8192, 7.5, 581.57\r\nNVIDIA RTX A6000, 49140, [N/A], 580.65.06\n" +
		"Tesla K80, 11441\n\nbad line\n, 100\nGPU, x\n")
	want := []GPU{
		{Name: "NVIDIA GeForce RTX 2080 SUPER", MemoryMB: 8192, ComputeCap: "7.5", Driver: "581.57"},
		{Name: "NVIDIA RTX A6000", MemoryMB: 49140, Driver: "580.65.06"},
		{Name: "Tesla K80", MemoryMB: 11441},
	}
	if got := parseNVIDIA(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseNVIDIA = %+v, want %+v", got, want)
	}
	if got := parseNVIDIA(nil); got != nil {
		t.Fatalf("no output: %+v", got)
	}
}

func TestNVIDIA(t *testing.T) {
	prev := nvidiaSMI
	t.Cleanup(func() { nvidiaSMI = prev })
	nvidiaSMI = func(_ context.Context, fields string) ([]byte, error) {
		if fields != smiFields {
			t.Errorf("asked for %q first", fields)
		}
		return []byte("NVIDIA L4, 23034, 8.9, 580.82\n"), nil
	}
	got := NVIDIA()
	if runtime.GOOS == "darwin" {
		if got != nil {
			t.Fatalf("a Mac has no NVIDIA GPU to ask about: %+v", got)
		}
		return
	}
	if len(got) != 1 || got[0].Name != "NVIDIA L4" || got[0].MemoryMB != 23034 || got[0].ComputeCap != "8.9" || got[0].Driver != "580.82" {
		t.Fatalf("NVIDIA = %+v", got)
	}
	// an nvidia-smi without compute_cap refuses the query: the name and memory then
	nvidiaSMI = func(_ context.Context, fields string) ([]byte, error) {
		if fields == smiFields {
			return nil, errors.New(`field "compute_cap" is not a valid field to query`)
		}
		return []byte("Tesla K80, 11441\n"), nil
	}
	if got := NVIDIA(); len(got) != 1 || got[0].Name != "Tesla K80" || got[0].ComputeCap != "" {
		t.Fatalf("an older driver: %+v", got)
	}
	nvidiaSMI = func(context.Context, string) ([]byte, error) { return nil, errors.New("no driver") }
	if got := NVIDIA(); got != nil {
		t.Fatalf("without the driver: %+v", got)
	}
}
