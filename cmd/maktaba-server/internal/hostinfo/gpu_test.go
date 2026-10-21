package hostinfo

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"testing"
)

func TestParseNVIDIA(t *testing.T) {
	out := []byte("NVIDIA GeForce RTX 2080 SUPER, 8192\r\nNVIDIA RTX A6000, 49140\n\nbad line\n, 100\nGPU, x\n")
	want := []GPU{{"NVIDIA GeForce RTX 2080 SUPER", 8192}, {"NVIDIA RTX A6000", 49140}}
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
	nvidiaSMI = func(context.Context) ([]byte, error) { return []byte("NVIDIA L4, 23034\n"), nil }
	got := NVIDIA()
	if runtime.GOOS == "darwin" {
		if got != nil {
			t.Fatalf("a Mac has no NVIDIA GPU to ask about: %+v", got)
		}
		return
	}
	if len(got) != 1 || got[0].Name != "NVIDIA L4" || got[0].MemoryMB != 23034 {
		t.Fatalf("NVIDIA = %+v", got)
	}
	nvidiaSMI = func(context.Context) ([]byte, error) { return nil, errors.New("no driver") }
	if got := NVIDIA(); got != nil {
		t.Fatalf("without the driver: %+v", got)
	}
}
