package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The staged pipeline says the version of its wheel, PEP 440's spelling of the
// release's (the release build stamps it: 1.5.0-rc.1 is 1.5.0rc1), so a pre-release
// passes the smoke and an environment holding another pipeline does not.
func TestSmokePipelineSaysTheWheelsVersion(t *testing.T) {
	ctx := context.Background()
	staged := t.TempDir()
	if err := smokePipeline(ctx, staged, "v1.5.0-rc.1"); err != nil {
		t.Fatalf("a release without its own environment: %v", err)
	}
	py := venvPython(filepath.Join(staged, "venv"))
	for _, f := range []string{py, filepath.Join(staged, "pipeline", "maktaba_pipeline-1.5.0rc1-py3-none-any.whl")} {
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var said []byte
	var ran string
	prev := pipelineVersionOf
	pipelineVersionOf = func(_ context.Context, p string) ([]byte, error) {
		ran = p
		return said, nil
	}
	t.Cleanup(func() { pipelineVersionOf = prev })

	said = []byte("some warning on stderr\n1.5.0rc1\n")
	if err := smokePipeline(ctx, staged, "v1.5.0-rc.1"); err != nil {
		t.Fatalf("the release's pipeline: %v", err)
	}
	if ran != py {
		t.Fatalf("ran %s, want the release's environment %s", ran, py)
	}
	said = []byte("1.4.0\n")
	var f *Failure
	if err := smokePipeline(ctx, staged, "v1.5.0-rc.1"); !errors.As(err, &f) || f.Code != "smoke_version" {
		t.Fatalf("another pipeline: %v, want smoke_version", err)
	}
}
