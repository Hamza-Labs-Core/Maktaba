package componentseed

import (
	"slices"
	"testing"
)

// TestTheMLRuntimeIsNotChroma: Chroma is retired (story 43.5); the search extra's
// embedder (sentence_transformers) and the transcription libraries are the ML runtime,
// and an environment that has only chromadb left in it is not.
func TestTheMLRuntimeIsNotChroma(t *testing.T) {
	if slices.Contains(mlModules, "chromadb") {
		t.Errorf("mlModules = %v: chromadb is no longer the pipeline's", mlModules)
	}
	for _, m := range []string{"sentence_transformers", "mlx_whisper", "faster_whisper"} {
		if !slices.Contains(mlModules, m) {
			t.Errorf("mlModules = %v lacks %s", mlModules, m)
		}
	}
}
