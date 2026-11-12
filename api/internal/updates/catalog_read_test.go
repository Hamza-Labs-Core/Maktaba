package updates

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func readDiscoverer(t *testing.T, h http.HandlerFunc) *Discoverer {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Discoverer{BaseURL: srv.URL, Repo: "o/r", HTTP: srv.Client()}
}

// A file of exactly the limit is read; one byte more is refused, whatever was
// made of the part that was read.
func TestGetLimit(t *testing.T) {
	body := []byte(strings.Repeat("a", 1000))
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	ctx := context.Background()
	b, code, err := d.get(ctx, d.BaseURL+"/f", false, 1000)
	if err != nil || code != http.StatusOK || !bytes.Equal(b, body) {
		t.Fatalf("at the limit: %d bytes, status %d, err %v", len(b), code, err)
	}
	if b, _, err = d.get(ctx, d.BaseURL+"/f", false, 5000); err != nil || !bytes.Equal(b, body) {
		t.Fatalf("under the limit: %d bytes, err %v", len(b), err)
	}
	b, code, err = d.get(ctx, d.BaseURL+"/f", false, 999)
	if err == nil || !strings.Contains(err.Error(), "larger than 999 bytes") || b != nil || code != http.StatusOK {
		t.Fatalf("over the limit: %d bytes, status %d, err %v", len(b), code, err)
	}
	// a use that swallowed the error of its reader does not hide it
	_, err = d.read(ctx, d.BaseURL+"/f", false, 10, func(r io.Reader) error {
		_, _ = io.ReadAll(r)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "larger than 10 bytes") {
		t.Fatalf("over the limit, error swallowed: err %v", err)
	}
	// a use that stopped before the limit is not refused for what it never read
	if _, err = d.read(ctx, d.BaseURL+"/f", false, 10, func(r io.Reader) error {
		_, err := io.ReadFull(r, make([]byte, 10))
		return err
	}); err != nil {
		t.Fatalf("read up to the limit and no further: %v", err)
	}
}

// An answer that breaks off is the request's error, not a list that does not decode.
func TestListBrokenOff(t *testing.T) {
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "5000")
		_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0"},`))
		// the handler returns with 5000 bytes promised: the connection is cut
	})
	rels, decodeErr, err := d.list(context.Background())
	if err == nil || decodeErr != nil || rels != nil {
		t.Fatalf("rels %v, decodeErr %v, err %v; want the request's error alone", rels, decodeErr, err)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want an unexpected end", err)
	}
}

func TestListNotAList(t *testing.T) {
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"message":"no"}`)) })
	rels, decodeErr, err := d.list(context.Background())
	if err != nil || decodeErr == nil || rels != nil {
		t.Fatalf("rels %v, decodeErr %v, err %v; want a decode error alone", rels, decodeErr, err)
	}
}

func TestListTooLong(t *testing.T) {
	// valid JSON, longer than the 8 MiB the list may be
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"tag_name":"v1.0.0","body":"`))
		_, _ = w.Write(bytes.Repeat([]byte("x"), 9<<20))
		_, _ = w.Write([]byte(`"}]`))
	})
	_, decodeErr, err := d.list(context.Background())
	if err == nil || decodeErr != nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("decodeErr %v, err %v; want the request's error: larger than the limit", decodeErr, err)
	}
}

// The check reads GitHub's list of 30 releases (about 7 MB, some 150 assets each) at
// every start and every check. Read whole and then decoded it allocated more than
// twice its size in one burst, and the API's resident memory stayed some 35 MB higher
// for good (the Core home's idle memory budget, tools/release/bundle-size). Read one
// release at a time it allocates less than the list is long.
func TestListAllocatesLessThanTheList(t *testing.T) {
	var body bytes.Buffer
	pad := strings.Repeat("x", 1400)
	body.WriteByte('[')
	for i := range 30 {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"tag_name":"v1.%d.0","html_url":"https://github.com/o/r/releases/tag/v1.%d.0","published_at":"2026-10-01T00:00:00Z","body":%q,"assets":[`,
			i, i, strings.Repeat("notes ", 400))
		for j := range 150 {
			if j > 0 {
				body.WriteByte(',')
			}
			fmt.Fprintf(&body, `{"name":"maktaba-%d-%d.tar.gz","size":%d,"browser_download_url":"https://github.com/o/r/releases/download/v1.%d.0/maktaba-%d-%d.tar.gz","uploader":{"login":%q}}`,
				i, j, 1000+j, i, i, j, pad)
		}
		body.WriteString(`]}`)
	}
	body.WriteByte(']')
	list := body.Bytes()
	if len(list) < 6<<20 || len(list) > 8<<20 {
		t.Fatalf("the list is %d bytes; the test wants one the size of the real one, under the 8 MiB cap", len(list))
	}
	d := readDiscoverer(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(list) })

	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	rels, decodeErr, err := d.list(context.Background())
	runtime.ReadMemStats(&b)
	if err != nil || decodeErr != nil {
		t.Fatal(err, decodeErr)
	}
	if len(rels) != 30 || len(rels[29].Assets) != 150 || rels[29].TagName != "v1.29.0" ||
		rels[29].Assets[149].Name != "maktaba-29-149.tar.gz" || rels[29].Assets[149].Size != 1149 || rels[29].PublishedAt.IsZero() {
		t.Fatalf("the list was not read whole: %d releases", len(rels))
	}
	// every goroutine's allocations, so the test server's too: small beside the list
	n := b.TotalAlloc - a.TotalAlloc
	t.Logf("a %d byte list: %d bytes allocated", len(list), n)
	if n > uint64(len(list))/2 {
		t.Fatalf("reading a %d byte list allocated %d bytes: more than half its length, so it is not read one release at a time", len(list), n)
	}
}
