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

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"what"}, {"extract"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Errorf("%v: no error", args)
		}
	}
}
