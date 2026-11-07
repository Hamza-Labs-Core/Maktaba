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

// Story 54.22: a built-in plugin's manifest is read with its trusted keys only in the
// trusted mode, which only the release's own bundle gets; its id must be Maktaba's.
func TestValidateTrusted(t *testing.T) {
	src := []byte(`schema = 1
id = "tv.maktaba.toy"
version = "0.0.0-dev"
plugin_api = "1.0"
maktaba = ">=0.0.0"
licence = "AGPL-3.0-only"
runtime = "native-python"

[strings]
name = "toy.name"
summary = "toy.summary"

[trusted]
runner = "light"
db.write = ["fp_toy_out"]

[[stage]]
name = "promo_links"
subject = "dup_group"
export = "x"
version = 1
pool = "cpu"
entry = "fp_toy.stage:IMPL"
may_request = ["clip_groups"]
`)
	m, errs := ValidateTrusted(src, nil)
	if len(errs) > 0 {
		t.Fatalf("a built-in manifest: %v", errs)
	}
	if m["runtime"] != "native-python" {
		t.Fatalf("normalised %v", m)
	}
	if _, errs := Validate(src, nil); !errs.Has(CodeTrustedField) {
		t.Fatalf("the same manifest as a third party's: %v", errs)
	}
	third := []byte(strings.Replace(string(src), "tv.maktaba.toy", "dev.example.toy", 1))
	if _, errs := ValidateTrusted(third, nil); !errs.Has(CodeReservedID) {
		t.Fatalf("a trusted manifest outside tv.maktaba.: %v", errs)
	}
	if !ReservedStage("transcribe") || ReservedStage("find_silences") {
		t.Fatal("reserved stages")
	}
}
