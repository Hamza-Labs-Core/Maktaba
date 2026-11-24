package releaseverify_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

const fixture161 = "testdata/feed/v0.3.161"

// realRelease161 is v0.3.161 as GitHub published it (`gh release view` and its three
// signed files, recorded 2026-11-24), verified with the embedded trusted root as a home
// verifies it.
func realRelease161(t *testing.T) (releaseverify.GHReleaseView, releaseverify.FeedSource) {
	t.Helper()
	read := func(n string) []byte {
		b, err := os.ReadFile(filepath.Join(fixture161, n))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var view releaseverify.GHReleaseView
	if err := json.Unmarshal(read("release-view.json"), &view); err != nil {
		t.Fatal(err)
	}
	id, _ := releaseverify.NewIdentity("")
	res, err := releaseverify.Verify(releaseverify.Input{Tag: view.TagName, Identity: id,
		Checksums: read("checksums.txt"), Bundle: read("checksums.txt.sigstore.json"), Manifest: read("release.json")})
	if err != nil {
		t.Fatalf("the recorded v0.3.161 does not verify: %v", err)
	}
	return view, view.Source(res, sha256Of(read("checksums.txt.sigstore.json")))
}

// retag is src as if it had been published at tag: its files at tag's addresses, each
// with a digest of its own (a full window's feed compresses no better than a real one).
func retag(src releaseverify.FeedSource, tag string) releaseverify.FeedSource {
	out := src
	out.Tag = tag
	out.Assets = nil
	for _, a := range src.Assets {
		a.URL = strings.Replace(a.URL, "/download/"+src.Tag+"/", "/download/"+tag+"/", 1)
		out.Assets = append(out.Assets, a)
	}
	res := *src.Verified
	res.Checksums = releaseverify.Checksums{}
	for n := range src.Verified.Checksums {
		res.Checksums[n] = sha256Of([]byte(tag + n))
	}
	res.ChecksumsSHA256 = sha256Of([]byte(tag))
	out.Verified = &res
	out.BundleSHA256 = sha256Of([]byte(tag + "bundle"))
	return out
}

func TestFeedFromTheRealV0_3_161(t *testing.T) {
	view, src := realRelease161(t)
	if len(view.Assets) != 190 {
		t.Fatalf("the fixture lists %d files, want the 190 GitHub listed", len(view.Assets))
	}
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	feeds, skips, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: releaseverify.DefaultRepo,
		Channel: releaseverify.FeedStable, GeneratedAt: at, Releases: []releaseverify.FeedSource{src}})
	if err != nil {
		t.Fatal(err)
	}
	if len(skips) != 0 {
		t.Fatalf("skips: %+v", skips)
	}
	var plats []string
	for p := range feeds {
		plats = append(plats, p)
	}
	want := []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64"}
	if !sameSet(plats, want) {
		t.Fatalf("platforms %v, want %v", plats, want)
	}
	names := func(p string) []string {
		var out []string
		for _, a := range feeds[p].Releases[0].Assets {
			out = append(out, a.Name)
		}
		return out
	}
	common := []string{"checksums.txt", "checksums.txt.sigstore.json", "release.json",
		"maktaba_pipeline-0.3.161-py3-none-any.whl", "maktaba-pipeline-0.3.161-constraints.txt",
		"maktaba-pipeline-0.3.161-constraints-core.txt", "maktaba-pipeline-0.3.161-constraints-stt.txt",
		"maktaba-pipeline-0.3.161-constraints-cuda.txt",
		"maktaba-migrations-0.3.161.tar.gz", "components-0.3.161.json", "latest.json"}
	for p, extra := range map[string][]string{
		"linux-amd64":   {"maktaba-server-0.3.161-linux-amd64.tar.gz", "maktaba-linux-payload-0.3.161-x86_64.tar.gz"},
		"linux-arm64":   {"maktaba-server-0.3.161-linux-arm64.tar.gz", "maktaba-linux-payload-0.3.161-aarch64.tar.gz"},
		"darwin-arm64":  {"maktaba-server-0.3.161-darwin-arm64.tar.gz"},
		"darwin-amd64":  {"maktaba-server-0.3.161-darwin-amd64.tar.gz"},
		"windows-amd64": {"maktaba-server-0.3.161-windows-amd64.zip"},
	} {
		if got := names(p); !sameSet(got, append(append([]string{}, common...), extra...)) {
			t.Errorf("%s lists %v", p, got)
		}
	}
	// every digest is the signed one, every address the release's own
	r := feeds["linux-amd64"].Releases[0]
	if r.Version != "0.3.161" || r.ChecksumsSHA256 != src.Verified.ChecksumsSHA256 ||
		r.ManifestSHA256 != src.Verified.Checksums["release.json"] || !r.PublishedAt.Equal(view.PublishedAt) {
		t.Fatalf("entry %+v", r)
	}
	for _, a := range r.Assets {
		want := src.Verified.Checksums[a.Name]
		switch a.Name {
		case "checksums.txt":
			want = src.Verified.ChecksumsSHA256
		case "checksums.txt.sigstore.json":
			want = src.BundleSHA256
		}
		if a.SHA256 != want || a.Size <= 0 ||
			a.URL != "https://github.com/Hamza-Labs-Core/Maktaba/releases/download/v0.3.161/"+a.Name {
			t.Errorf("%+v (want sha256 %q)", a, want)
		}
	}
	for p, f := range feeds {
		b, err := releaseverify.MarshalFeed(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := releaseverify.ParseFeed(b, releaseverify.DefaultRepo, releaseverify.FeedStable, p, ""); err != nil {
			t.Fatalf("%s: the builder's own feed does not parse: %v", p, err)
		}
	}
}

// TestFeedSizeOfAFullWindow: ten releases the size of v0.3.161 (190 files each: thirty
// of them were GitHub's 10.5 MB list that broke every home's check) make a platform feed
// of some 25 KB, a few KiB as served compressed, and far under MaxFeed.
func TestFeedSizeOfAFullWindow(t *testing.T) {
	_, src := realRelease161(t)
	var rels []releaseverify.FeedSource
	for i := 152; i <= 161; i++ {
		rels = append(rels, retag(src, fmt.Sprintf("v0.3.%d", i)))
	}
	feeds, _, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: releaseverify.DefaultRepo,
		Channel: releaseverify.FeedStable, GeneratedAt: time.Now(), Releases: rels})
	if err != nil {
		t.Fatal(err)
	}
	for p, f := range feeds {
		if len(f.Releases) != 10 {
			t.Fatalf("%s lists %d", p, len(f.Releases))
		}
		b, _ := releaseverify.MarshalFeed(f)
		var z bytes.Buffer
		w := gzip.NewWriter(&z)
		_, _ = w.Write(b)
		_ = w.Close()
		t.Logf("%s: %d bytes, %d gzipped", p, len(b), z.Len())
		if len(b) > 48<<10 || z.Len() > 12<<10 {
			t.Errorf("%s: %d bytes (%d gzipped), want under 48 KiB (12 KiB)", p, len(b), z.Len())
		}
		if len(b) > releaseverify.MaxFeed/8 {
			t.Errorf("%s is within 8× of the cap", p)
		}
	}
}

func TestFeedKeepsTheNewestAndMarksWithdrawn(t *testing.T) {
	_, src := realRelease161(t)
	var rels []releaseverify.FeedSource
	for i := 140; i <= 161; i++ {
		rels = append(rels, retag(src, fmt.Sprintf("v0.3.%d", i)))
	}
	pre := retag(src, "v0.3.162-rc.1")
	pre.Prerelease = true
	rels = append(rels, pre)
	b := releaseverify.FeedBuild{Repo: releaseverify.DefaultRepo, Channel: releaseverify.FeedStable, GeneratedAt: time.Now(),
		Keep: 3, Releases: rels, Withdrawn: map[string]string{"0.3.160": "rolled back on homes", "0.3.150": "old"}}
	feeds, _, err := releaseverify.BuildFeeds(b)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range feeds["linux-amd64"].Releases {
		s := r.Version
		if r.Withdrawn != nil {
			s += " withdrawn: " + r.Withdrawn.Reason
		}
		got = append(got, s)
	}
	want := []string{"0.3.161", "0.3.160 withdrawn: rolled back on homes", "0.3.159", "0.3.158"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stable lists %v, want %v", got, want)
	}
	b.Channel = releaseverify.FeedBeta
	feeds, _, _ = releaseverify.BuildFeeds(b)
	if r := feeds["linux-amd64"].Releases[0]; r.Tag != "v0.3.162-rc.1" || !r.Prerelease {
		t.Fatalf("beta's newest is %+v", r)
	}
}

// signedFeed is the real v0.3.161 feed for linux-amd64 signed by the fake Sigstore as o.
func signedFeed(t *testing.T, ss *releaseverifytest.Sigstore, o releaseverifytest.SignOptions) (data, bundle []byte) {
	t.Helper()
	_, src := realRelease161(t)
	feeds, _, err := releaseverify.BuildFeeds(releaseverify.FeedBuild{Repo: releaseverify.DefaultRepo,
		Channel: releaseverify.FeedStable, GeneratedAt: time.Now(), Releases: []releaseverify.FeedSource{src}})
	if err != nil {
		t.Fatal(err)
	}
	data, err = releaseverify.MarshalFeed(feeds["linux-amd64"])
	if err != nil {
		t.Fatal(err)
	}
	bundle, err = ss.SignBlob(data, o)
	if err != nil {
		t.Fatal(err)
	}
	return data, bundle
}

func TestVerifyFeedSignature(t *testing.T) {
	ss := releaseverifytest.MustNew()
	tr, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := releaseverify.NewIdentity("")
	repo := releaseverify.DefaultRepo
	check := func(data, bundle []byte) error {
		_, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
			TrustedMaterial: tr, Channel: "stable", Platform: "linux-amd64"})
		return err
	}
	for _, trig := range []string{"workflow_run", "workflow_dispatch", "schedule"} {
		data, bundle := signedFeed(t, ss, releaseverifytest.GitHubFeed(repo, trig))
		f, signedAt, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
			TrustedMaterial: tr, Channel: "stable", Platform: "linux-amd64"})
		if err != nil {
			t.Fatalf("%s: %v", trig, err)
		}
		if f.Releases[0].Tag != "v0.3.161" || signedAt.IsZero() {
			t.Fatalf("%s: %+v at %v", trig, f.Releases[0].Tag, signedAt)
		}
	}
	data, bundle := signedFeed(t, ss, releaseverifytest.GitHubFeed(repo, "workflow_run"))

	// tampered: one byte of the feed changed after signing
	bad := bytes.Replace(data, []byte(`"0.3.161"`), []byte(`"0.3.169"`), 1)
	if err := check(bad, bundle); !errors.Is(err, releaseverify.ErrSignature) {
		t.Errorf("tampered feed: %v, want ErrSignature", err)
	}
	// missing or broken bundle
	if err := check(data, nil); !errors.Is(err, releaseverify.ErrSignature) {
		t.Errorf("no bundle: %v", err)
	}
	if err := check(data, []byte("{")); !errors.Is(err, releaseverify.ErrSignature) {
		t.Errorf("broken bundle: %v", err)
	}
	// signed, but by someone else
	branch := releaseverifytest.GitHubFeed(repo, "workflow_dispatch")
	branch.SAN = strings.Replace(branch.SAN, "refs/heads/main", "refs/heads/evil", 1)
	branch.BuildConfigURI, branch.Ref = branch.SAN, "refs/heads/evil"
	pr := releaseverifytest.GitHubFeed(repo, "pull_request")
	other := releaseverifytest.GitHubFeed("someone/Maktaba", "workflow_run")
	for name, o := range map[string]releaseverifytest.SignOptions{
		"the release workflow": releaseverifytest.GitHubRelease(repo, "v0.3.161"),
		"a branch":             branch,
		"a pull request":       pr,
		"a fork":               other,
		"a self-hosted runner": func() releaseverifytest.SignOptions {
			o := releaseverifytest.GitHubFeed(repo, "workflow_run")
			o.RunnerEnvironment = "self-hosted"
			return o
		}(),
	} {
		d, b := signedFeed(t, ss, o)
		if err := check(d, b); !errors.Is(err, releaseverify.ErrIdentity) {
			t.Errorf("%s: %v, want ErrIdentity", name, err)
		}
	}
	// no transparency log proof
	nolog := releaseverifytest.GitHubFeed(repo, "workflow_run")
	nolog.NoInclusionProof = true
	if d, b := signedFeed(t, ss, nolog); !errors.Is(check(d, b), releaseverify.ErrTlog) {
		t.Errorf("no inclusion proof: %v", check(d, b))
	}
	// a real root does not trust the fake Sigstore
	if _, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: data, Bundle: bundle, Identity: id,
		Channel: "stable", Platform: "linux-amd64"}); err == nil {
		t.Error("the embedded root accepted a fake Sigstore's feed")
	}
}

func TestVerifyFeedRefusesWhatItDoesNotAskFor(t *testing.T) {
	ss := releaseverifytest.MustNew()
	tr, _ := ss.TrustedRoot()
	id, _ := releaseverify.NewIdentity("")
	o := releaseverifytest.GitHubFeed(releaseverify.DefaultRepo, "workflow_run")
	data, bundle := signedFeed(t, ss, o)
	for name, in := range map[string]releaseverify.FeedInput{
		"another platform's feed": {Channel: "stable", Platform: "darwin-arm64"},
		"another channel's feed":  {Channel: "beta", Platform: "linux-amd64"},
		"another download host":   {Channel: "stable", Platform: "linux-amd64", DownloadBase: "https://mirror.example"},
	} {
		in.Data, in.Bundle, in.Identity, in.TrustedMaterial = data, bundle, id, tr
		if _, _, err := releaseverify.VerifyFeed(in); !errors.Is(err, releaseverify.ErrFeed) {
			t.Errorf("%s: %v, want ErrFeed", name, err)
		}
	}
	// signed, but saying what a feed may not: each edited and signed again
	edit := func(f func(m map[string]any)) ([]byte, []byte) {
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		f(m)
		b, _ := json.Marshal(m)
		bun, err := ss.SignBlob(b, o)
		if err != nil {
			t.Fatal(err)
		}
		return b, bun
	}
	rel := func(m map[string]any) map[string]any { return m["releases"].([]any)[0].(map[string]any) }
	for name, f := range map[string]func(m map[string]any){
		"a file elsewhere": func(m map[string]any) {
			rel(m)["assets"].([]any)[3].(map[string]any)["url"] = "https://evil.example/maktaba-server.tar.gz"
		},
		"a path out of the release": func(m map[string]any) {
			rel(m)["assets"].([]any)[3].(map[string]any)["name"] = "../x"
		},
		"a pre-release on stable": func(m map[string]any) { rel(m)["prerelease"] = true },
		"no checksums.txt": func(m map[string]any) {
			rel(m)["assets"] = rel(m)["assets"].([]any)[1:]
		},
		"a release twice":     func(m map[string]any) { m["releases"] = append(m["releases"].([]any), rel(m)) },
		"another repository":  func(m map[string]any) { m["repo"] = "someone/Maktaba" },
		"another schema":      func(m map[string]any) { m["schema"] = 2 },
		"a bad tag":           func(m map[string]any) { rel(m)["tag"] = "latest" },
		"withdrawn, no cause": func(m map[string]any) { rel(m)["withdrawn"] = map[string]any{"reason": ""} },
	} {
		d, b := edit(f)
		_, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: d, Bundle: b, Identity: id,
			TrustedMaterial: tr, Channel: "stable", Platform: "linux-amd64"})
		if !errors.Is(err, releaseverify.ErrFeed) {
			t.Errorf("%s: %v, want ErrFeed", name, err)
		}
	}
	// too large: refused before its signature is looked at
	big := append(bytes.Repeat([]byte(" "), releaseverify.MaxFeed), data...)
	if _, _, err := releaseverify.VerifyFeed(releaseverify.FeedInput{Data: big, Bundle: bundle, Identity: id,
		TrustedMaterial: tr, Channel: "stable", Platform: "linux-amd64"}); !errors.Is(err, releaseverify.ErrFeed) {
		t.Errorf("too large: %v", err)
	}
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, n := range m {
		if n != 0 {
			return false
		}
	}
	return true
}

func sha256Of(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
