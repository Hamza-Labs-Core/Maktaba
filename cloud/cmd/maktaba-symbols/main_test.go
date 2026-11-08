package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/symbols/symbolstest"
)

func TestExtractWritesOneTablePerArchitecture(t *testing.T) {
	dir, fixture := t.TempDir(), symbolstest.Fixture(t, "crashy.dwarf")
	var out strings.Builder
	if err := run(context.Background(), []string{"extract", "-o", dir, fixture}, &out); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(dir, "*.symbols"))
	if len(files) != 2 {
		t.Fatalf("tables = %v (%s)", files, out.String())
	}
	b, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if tab, err := symbols.Decode(b); err != nil || filepath.Base(files[0]) != tab.DebugID+".symbols" {
		t.Fatalf("%s: %v", files[0], err)
	}
}

func TestUploadSendsEachTableWithTheToken(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		tab, err := symbols.Decode(b)
		if r.URL.Path != "/v1/symbols" || r.URL.Query().Get("version") != "0.3.98" ||
			r.Header.Get("Authorization") != "Bearer tok" || err != nil {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, tab.Arch)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	fixture := symbolstest.Fixture(t, "crashy.dwarf")
	t.Setenv("MAKTABA_SYMBOLS_TOKEN", "tok")
	var out strings.Builder
	if err := run(context.Background(), []string{"upload", "-url", srv.URL + "/", "-version", "0.3.98", fixture}, &out); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.Contains(out.String(), "uploaded") {
		t.Fatalf("uploaded %v (%s)", got, out.String())
	}
	// the cloud's refusal is the command's error
	t.Setenv("MAKTABA_SYMBOLS_TOKEN", "wrong")
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, "-version", "0.3.98", fixture}, io.Discard); err == nil ||
		!strings.Contains(err.Error(), "400") {
		t.Fatalf("a refused upload: %v", err)
	}
	t.Setenv("MAKTABA_SYMBOLS_TOKEN", "")
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, fixture}, io.Discard); err == nil {
		t.Fatal("an upload with no token ran")
	}
}

// The tables extract wrote upload as they are (a release that could not reach the cloud keeps
// them as an artifact); a file that is not a table is refused before anything is sent.
func TestUploadTakesExtractedTables(t *testing.T) {
	var mu sync.Mutex
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		tab, err := symbols.Decode(b)
		if err != nil {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		mu.Lock()
		got = append(got, tab.DebugID)
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := run(context.Background(), []string{"extract", "-o", dir, symbolstest.Fixture(t, "crashy.dwarf")}, io.Discard); err != nil {
		t.Fatal(err)
	}
	tables, _ := filepath.Glob(filepath.Join(dir, "*.symbols"))
	t.Setenv("MAKTABA_SYMBOLS_TOKEN", "tok")
	if err := run(context.Background(), append([]string{"upload", "-url", srv.URL}, tables...), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0]+".symbols" != filepath.Base(tables[0]) {
		t.Fatalf("uploaded %v from %v", got, tables)
	}
	bad := filepath.Join(dir, "bad.symbols")
	if err := os.WriteFile(bad, []byte("not a table"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, bad}, io.Discard); err == nil || len(got) != 2 {
		t.Fatalf("a file that is not a table: %v, %d sent", err, len(got))
	}
}

// -r8: an Android release's mapping, uploaded as its app's table of that release; extract
// writes it like a dSYM's, and the kept table uploads as it is.
func TestUploadAnR8Mapping(t *testing.T) {
	var got []*symbols.Table
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		tab, err := symbols.Decode(b)
		if err != nil || r.URL.Query().Get("version") != "0.3.102" {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		got = append(got, tab)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	m := "../../internal/symbols/testdata/mapping.txt"
	t.Setenv("MAKTABA_SYMBOLS_TOKEN", "tok")
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, "-r8", "android.tv", "-version", "0.3.102", m}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != symbols.KindR8 || got[0].CodeFile != "android.tv" || got[0].DebugID != symbols.R8ID("android.tv", "0.3.102") {
		t.Fatalf("uploaded %+v", got)
	}
	dir := t.TempDir()
	var out strings.Builder
	if err := run(context.Background(), []string{"extract", "-o", dir, "-r8", "android.tv", "-version", "v0.3.102", m}, &out); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, symbols.R8ID("android.tv", "0.3.102")+".symbols")
	if !strings.Contains(out.String(), "r8 android.tv 0.3.102") {
		t.Fatalf("extract said %q", out.String())
	}
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, "-version", "0.3.102", kept}, io.Discard); err != nil || len(got) != 2 {
		t.Fatalf("the kept table: %v (%d)", err, len(got))
	}
	// no version, or a file that is not a mapping: nothing is sent
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, "-r8", "android.tv", m}, io.Discard); err == nil {
		t.Error("-r8 with no version ran")
	}
	if err := run(context.Background(), []string{"upload", "-url", srv.URL, "-r8", "android.tv", "-version", "1", "main.go"}, io.Discard); err == nil || len(got) != 2 {
		t.Errorf("a Go file as a mapping: %v", err)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"what"}, {"extract"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
