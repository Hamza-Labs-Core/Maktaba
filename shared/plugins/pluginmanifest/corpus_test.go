package pluginmanifest

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const corpus = "../testdata/manifests"

// packageFiles serves the corpus's shared package (i18n, ui) to the package rules.
func packageFiles(t *testing.T) Files {
	t.Helper()
	return func(p string) ([]byte, bool) {
		b, err := os.ReadFile(filepath.Join(corpus, "package", filepath.FromSlash(p)))
		return b, err == nil
	}
}

// expectation reads the "# expect: <code> [detail]" first line of an invalid case.
func expectation(t *testing.T, src []byte) (code, detail string) {
	t.Helper()
	line, _, _ := bytes.Cut(src, []byte("\n"))
	rest, ok := strings.CutPrefix(string(line), "# expect: ")
	if !ok {
		t.Fatalf("no expect line")
	}
	code, detail, _ = strings.Cut(strings.TrimSpace(rest), " ")
	return code, detail
}

// T01 (go): every valid manifest passes, every invalid one raises its named code.
// T02 (go): each valid one normalises to the golden JSON the other three must match.
func TestCorpus(t *testing.T) {
	files := packageFiles(t)
	valid, _ := filepath.Glob(filepath.Join(corpus, "valid", "*.toml"))
	if len(valid) == 0 {
		t.Fatal("no valid cases")
	}
	for _, p := range valid {
		name := strings.TrimSuffix(filepath.Base(p), ".toml")
		t.Run("valid/"+name, func(t *testing.T) {
			src, _ := os.ReadFile(p)
			m, errs := Validate(src, files)
			if errs != nil {
				t.Fatalf("refused: %v", errs)
			}
			got := Normalised(m)
			golden := filepath.Join(corpus, "normalised", name+".json")
			if os.Getenv("UPDATE_GOLDEN") == "1" {
				if err := os.WriteFile(golden, append(got, '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, bytes.TrimRight(want, "\n")) {
				t.Fatalf("normalised differs from %s:\n got %s\nwant %s", golden, got, want)
			}
		})
	}
	invalid, _ := filepath.Glob(filepath.Join(corpus, "invalid", "*.toml"))
	if len(invalid) == 0 {
		t.Fatal("no invalid cases")
	}
	for _, p := range invalid {
		t.Run("invalid/"+filepath.Base(p), func(t *testing.T) {
			src, _ := os.ReadFile(p)
			code, detail := expectation(t, src)
			_, errs := Validate(src, files)
			for _, e := range errs {
				if e.Code == code && (detail == "" || e.Detail == detail) {
					return
				}
			}
			t.Fatalf("want %s %s, got %v", code, detail, errs)
		})
	}
}

// T07: the queue name of a stage is stable per id and differs across ids.
func TestQueueName(t *testing.T) {
	a := QueueName("dev.example.silence-chapters", "find_silences")
	if a != QueueName("dev.example.silence-chapters", "find_silences") {
		t.Fatal("not stable")
	}
	if a == QueueName("dev.example.other", "find_silences") {
		t.Fatal("two ids gave one name")
	}
	if !strings.HasPrefix(a, "p_") || len(a) != len("p_12345678_find_silences") {
		t.Fatalf("shape: %s", a)
	}
	// the vector every language checks (shared/plugins/testdata/stage_names.json)
	if got := QueueName("dev.example.silence-chapters", "x"); got != stageNameVector {
		t.Fatalf("vector: %s", got)
	}
}

func TestManifestWithoutFilesSkipsPackageRules(t *testing.T) {
	src, _ := os.ReadFile(filepath.Join(corpus, "invalid", "string_missing_ar.toml"))
	if _, errs := Validate(src, nil); errs != nil {
		t.Fatalf("package rules ran without files: %v", errs)
	}
}

func TestNotTOML(t *testing.T) {
	_, errs := Validate([]byte("id = "), nil)
	if !errs.Has(CodeNotTOML) {
		t.Fatalf("got %v", errs)
	}
}

func TestEveryCodeHasAnInvalidCase(t *testing.T) {
	invalid, _ := filepath.Glob(filepath.Join(corpus, "invalid", "*.toml"))
	seen := map[string]bool{}
	for _, p := range invalid {
		src, _ := os.ReadFile(p)
		c, _ := expectation(t, src)
		seen[c] = true
	}
	for _, c := range []string{CodeUnknownKey, CodeMissingKey, CodeSchemaNewer, CodeReservedID,
		CodeBadPlatform, CodeBadRange, CodeBadCapability, CodeDuplicateCapability,
		CodeDuplicateStage, CodeBadRequest, CodeUnknownEvent, CodeSubjectMismatch,
		CodeBadSchedule, CodeMissingString, CodeStringTooLong, CodeMissingFile, CodeBadSettings} {
		if !seen[c] {
			t.Errorf("no corpus case raises %s", c)
		}
	}
}

// 54.39 §1: a flow.toml is read with the manifest's schema reader, against flow.schema.json;
// its refusals carry flow_* codes and say where.
func TestValidateFlowDoc(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "testdata", "flows", "chain_two_store", "flow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	doc, errs := ValidateFlowDoc(src)
	if errs != nil {
		t.Fatalf("refused: %v", errs)
	}
	members := doc["plugin"].([]any)
	stage := members[1].(map[string]any)["stage"].([]any)[1].(map[string]any)
	if len(members) != 2 || len(stage["emits"].([]any)) != 0 {
		t.Fatalf("defaults not filled: %v", stage)
	}
	for src, code := range map[string]string{
		strings.Replace(string(src), "schema = 1", "schema = 1\nnetwork = true", 1):         "flow_unknown_key",
		strings.Replace(string(src), `subject = "video"`, `subject = "user"`, 1):            "flow_invalid",
		strings.Replace(string(src), `triggers = ["audio.ready"]`, `triggers = ["Bad"]`, 1): "flow_invalid",
		"not toml = = =": "flow_invalid",
	} {
		_, errs := ValidateFlowDoc([]byte(src))
		if !errs.Has(code) {
			t.Errorf("want %s, got %v", code, errs)
		}
	}
}
