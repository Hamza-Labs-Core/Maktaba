package system

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDecodeJSONArray(t *testing.T) {
	type item struct {
		N int `json:"n"`
	}
	for _, tc := range []struct {
		name, in string
		want     []int
		bad      bool
	}{
		{name: "elements", in: ` [{"n":1}, {"n":2,"other":[1,{"x":"]"}]} ,{"n":3}] ` + "\n", want: []int{1, 2, 3}},
		{name: "empty", in: `[]`, want: []int{}},
		{name: "null", in: `null`},
		{name: "an object", in: `{"n":1}`, bad: true},
		{name: "a string", in: `"x"`, bad: true},
		{name: "cut short", in: `[{"n":1},{"n":`, bad: true},
		{name: "never closed", in: `[{"n":1}`, bad: true},
		{name: "data after it", in: `[{"n":1}] [`, bad: true},
		{name: "a second value after it", in: `[{"n":1}] 7`, bad: true},
		{name: "an element of another type", in: `[{"n":"one"}]`, bad: true},
		{name: "nothing", in: ``, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeJSONArray[item](strings.NewReader(tc.in))
			if tc.bad {
				if err == nil {
					t.Fatalf("no error; got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (got == nil) != (tc.want == nil) || len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %v", got, tc.want)
			}
			for i, w := range tc.want {
				if got[i].N != w {
					t.Fatalf("element %d = %d, want %d", i, got[i].N, w)
				}
			}
		})
	}
}

// A reader's own error comes back as it is (the caller tells a broken answer from
// one that does not decode).
func TestDecodeJSONArrayReaderError(t *testing.T) {
	_, err := DecodeJSONArray[int](io.MultiReader(strings.NewReader(`[1, 2, `), errReader{}))
	if err != errBroken {
		t.Fatalf("err = %v, want the reader's", err)
	}
}

var errBroken = fmt.Errorf("broken")

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errBroken }

// GitHubReleases is a releases list the size of the real one: n releases of
// assets assets each, about 1.7 kB an asset as GitHub describes one (most of it
// fields nothing here reads).
func gitHubReleases(n, assets int) []byte {
	var b bytes.Buffer
	pad := strings.Repeat("x", 1400)
	b.WriteByte('[')
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"url":"https://api.github.com/repos/o/r/releases/%d","tag_name":"v1.%d.0","html_url":"https://github.com/o/r/releases/tag/v1.%d.0","draft":false,"prerelease":false,"body":%q,"assets":[`,
			i, i, i, strings.Repeat("notes ", 400))
		for j := range assets {
			if j > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"name":"maktaba-%d-%d.tar.gz","size":%d,"browser_download_url":"https://github.com/o/r/releases/download/v1.%d.0/maktaba-%d-%d.tar.gz","uploader":{"login":%q}}`,
				i, j, 1000+j, i, i, j, pad)
		}
		b.WriteString(`]}`)
	}
	b.WriteByte(']')
	return b.Bytes()
}

// allocated is how many bytes f allocated (every goroutine's, so the test server's
// writes too: they are small beside what is measured).
func allocated(f func()) uint64 {
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	f()
	runtime.ReadMemStats(&b)
	return b.TotalAlloc - a.TotalAlloc
}

// The update check reads GitHub's list of 30 releases (about 7 MB: 150 assets each) at
// every start and every check. Decoded whole it allocated three times its size in one
// burst, and the API's resident memory stayed some 35 MB higher afterward. Reading one release at a
// time allocates less than the size of the list.
//
// The list here has the real one's 30 releases and its assets' shape, with 30 assets a
// release instead of 150 (about 1.5 MB). What one release at a time allocates (the
// releases kept, and a buffer of one release) grows with the assets as the list does: a
// third of the list here, a fifth at 150, against four and a half times it read whole.
// The 7 MB list decoded under -race took 1.2 s on a CI runner, three times the unit
// tier's per-test budget (tools/ci/test-budget), and failed main's runs on a slow one.
// The control below shows the bound still tells the two readings apart at this size.
func TestFetchReleasesAllocatesLessThanTheList(t *testing.T) {
	body := gitHubReleases(30, 30)
	if len(body) < 1<<20 || len(body) > 8<<20 {
		t.Fatalf("the list is %d bytes; the test wants one over 1 MiB and under the 8 MiB cap", len(body))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Channel: "stable", APIBaseURL: srv.URL, Now: time.Now})
	var rels []ghRelease
	var err error
	n := allocated(func() { rels, err = u.fetchReleases(context.Background()) })
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 30 || len(rels[29].Assets) != 30 || rels[29].TagName != "v1.29.0" ||
		rels[29].Assets[29].Name != "maktaba-29-29.tar.gz" || rels[29].Assets[29].Size != 1029 {
		t.Fatalf("the list was not read whole: %d releases", len(rels))
	}
	t.Logf("a %d byte list: %d bytes allocated", len(body), n)
	if n > uint64(len(body))/2 {
		t.Fatalf("reading a %d byte list allocated %d bytes: more than half its length, so it is not read one release at a time", len(body), n)
	}

	// the control: the same list decoded whole is over the bound, so the reading above
	// passes because it is one release at a time, not because the list is small
	var whole []ghRelease
	w := allocated(func() {
		b, err := io.ReadAll(bytes.NewReader(body))
		if err == nil {
			err = json.Unmarshal(b, &whole)
		}
		if err != nil {
			t.Error(err)
		}
	})
	t.Logf("the same list read whole: %d bytes allocated", w)
	if len(whole) != 30 || w <= uint64(len(body))/2 {
		t.Fatalf("read whole, the %d byte list allocated %d bytes: the bound would not tell the readings apart", len(body), w)
	}
}

// The check asks for the newest 10 releases: GitHub's 30 (some 190 assets each) passed
// the 8 MiB cap on 2026-11-23, and every check failed from then on.
func TestFetchReleasesAsksForTen(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = w.Write(gitHubReleases(10, 3))
	}))
	t.Cleanup(srv.Close)
	u := NewUpdater(UpdaterConfig{CurrentVersion: "v1.0.0", Channel: "stable", APIBaseURL: srv.URL, Now: time.Now})
	rels, err := u.fetchReleases(context.Background())
	if err != nil || len(rels) != 10 {
		t.Fatalf("read %d releases: %v", len(rels), err)
	}
	if query != "per_page=10" {
		t.Fatalf("asked %q, want per_page=10", query)
	}
}
