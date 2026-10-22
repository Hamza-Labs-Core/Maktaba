package releaseverify_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	rv "github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
	"github.com/sigstore/sigstore-go/pkg/root"
)

const (
	repo     = "Hamza-Labs-Core/Maktaba"
	tag      = "v1.5.0"
	platform = "linux/amd64"
)

type fixture struct {
	ss  *releaseverifytest.Sigstore
	tm  *root.TrustedRoot
	rel *releaseverifytest.FakeRelease
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ss, err := releaseverifytest.New()
	if err != nil {
		t.Fatal(err)
	}
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := ss.NewRelease(repo, tag, platform)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{ss: ss, tm: tm, rel: rel}
}

func (f *fixture) input() rv.Input {
	id, _ := rv.NewIdentity(repo)
	r := f.rel
	return rv.Input{
		Tag: r.Tag, Identity: id, TrustedMaterial: f.tm,
		Checksums: r.Files["checksums.txt"], Bundle: r.Files["checksums.txt.sigstore.json"], Manifest: r.Files["release.json"],
		Artifacts: []rv.Artifact{
			{Name: r.Archive, Data: r.Files[r.Archive]},
			{Name: r.Wheel, Data: r.Files[r.Wheel]},
			{Name: r.Cons, Data: r.Files[r.Cons]},
		},
	}
}

func wantKind(t *testing.T, err, kind error) {
	t.Helper()
	if err == nil {
		t.Fatalf("verified; want %v", kind)
	}
	if !errors.Is(err, kind) {
		t.Fatalf("err = %v; want kind %v", err, kind)
	}
	var e *rv.Error
	if !errors.As(err, &e) {
		t.Fatalf("err %T is not *releaseverify.Error", err)
	}
}

func TestVerifyValidFixture(t *testing.T) {
	f := newFixture(t)
	res, err := rv.Verify(f.input())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Manifest.Version != "1.5.0" || res.Manifest.Tag != tag {
		t.Fatalf("manifest = %+v", res.Manifest)
	}
	if want := "https://github.com/" + repo + "/.github/workflows/release.yml@refs/tags/" + tag; res.Identity != want {
		t.Fatalf("identity = %q, want %q", res.Identity, want)
	}
	if res.SignedAt.IsZero() || time.Since(res.SignedAt) > time.Hour {
		t.Fatalf("signed at = %v", res.SignedAt)
	}
	if len(res.ChecksumsSHA256) != 64 {
		t.Fatalf("checksums digest = %q", res.ChecksumsSHA256)
	}
	// Step 6: extracted binaries against release.json.
	for name, b := range f.rel.Binaries {
		if err := res.Manifest.VerifyExtracted(platform, name, b); err != nil {
			t.Fatalf("VerifyExtracted(%s): %v", name, err)
		}
	}
	wantKind(t, res.Manifest.VerifyExtracted(platform, "maktaba-api", []byte("evil")), rv.ErrChecksum)
}

// The bundle round-trips through files and VerifyDir the way the launcher's
// offline re-check and release.yml's self-check use it.
func TestVerifyDirReleaseAssets(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	if err := f.rel.WriteDir(dir); err != nil {
		t.Fatal(err)
	}
	id, _ := rv.NewIdentity(repo)
	if _, err := rv.VerifyDir(dir, tag, id, rv.ReleaseAssets, "", f.tm); err != nil {
		t.Fatalf("VerifyDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, f.rel.Wheel), []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := rv.VerifyDir(dir, tag, id, rv.ReleaseAssets, "", f.tm)
	wantKind(t, err, rv.ErrChecksum)
}

func TestVerifyDirInstalled(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	for _, n := range []string{"checksums.txt", "checksums.txt.sigstore.json", "release.json"} {
		if err := os.WriteFile(filepath.Join(dir, n), f.rel.Files[n], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for n, b := range f.rel.Binaries {
		if err := os.WriteFile(filepath.Join(dir, n), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	id, _ := rv.NewIdentity(repo)
	if _, err := rv.VerifyDir(dir, tag, id, rv.InstalledRelease, platform, f.tm); err != nil {
		t.Fatalf("VerifyDir installed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "maktaba-streaming"), []byte("swapped"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := rv.VerifyDir(dir, tag, id, rv.InstalledRelease, platform, f.tm)
	wantKind(t, err, rv.ErrChecksum)
}

func resigned(t *testing.T, f *fixture, mut func(*releaseverifytest.SignOptions)) rv.Input {
	t.Helper()
	o := releaseverifytest.GitHubRelease(repo, tag)
	mut(&o)
	if err := f.rel.Resign(f.ss, o); err != nil {
		t.Fatal(err)
	}
	return f.input()
}

func TestVerifyWrongSAN(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) {
		o.SAN = "https://github.com/" + repo + "/.github/workflows/ci.yml@refs/tags/" + tag
	}))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifyOtherTag(t *testing.T) {
	f := newFixture(t)
	// A valid signature of the same files by the workflow run of another tag.
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) {
		*o = releaseverifytest.GitHubRelease(repo, "v1.4.0")
	}))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifyBranchRef(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) {
		o.SAN = "https://github.com/" + repo + "/.github/workflows/release.yml@refs/heads/main"
		o.Ref = "refs/heads/main"
	}))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifyWrongTrigger(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) { o.Trigger = "workflow_dispatch" }))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifyWrongIssuer(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) { o.Issuer = "https://accounts.google.com" }))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifyForkRepo(t *testing.T) {
	f := newFixture(t)
	// The fork's own release workflow, signing the same checksums: the old
	// release-notes regexp https://github.com/.+/Maktaba/… accepted this.
	in := resigned(t, f, func(o *releaseverifytest.SignOptions) {
		*o = releaseverifytest.GitHubRelease("evil-fork/Maktaba", tag)
	})
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrIdentity)

	// Only the startup setting retargets the identity, repo and SAN together.
	forkID, err := rv.NewIdentity("evil-fork/Maktaba")
	if err != nil {
		t.Fatal(err)
	}
	in.Identity = forkID
	if _, err := rv.Verify(in); err != nil {
		t.Fatalf("fork identity verifying its own release: %v", err)
	}
}

func TestVerifyTamperedChecksums(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Checksums = append([]byte{}, in.Checksums...)
	in.Checksums = append(in.Checksums, []byte(strings.Repeat("0", 64)+"  ./extra.bin\n")...)
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrSignature)
}

func TestVerifyTamperedArchive(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Artifacts[0].Data = append(append([]byte{}, in.Artifacts[0].Data...), 0)
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrChecksum)
}

func TestVerifyUnlistedArtifact(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Artifacts = append(in.Artifacts, rv.Artifact{Name: "maktaba-server-evil.tar.gz", Data: []byte("x")})
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrChecksum)
}

func TestVerifyNoTlog(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) { o.NoTlog = true }))
	wantKind(t, err, rv.ErrTlog)
}

func TestVerifyNoInclusionProof(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) { o.NoInclusionProof = true }))
	wantKind(t, err, rv.ErrTlog)
}

func TestVerifyTamperedInclusionProof(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	var b map[string]any
	if err := json.Unmarshal(in.Bundle, &b); err != nil {
		t.Fatal(err)
	}
	vm := b["verificationMaterial"].(map[string]any)
	e := vm["tlogEntries"].([]any)[0].(map[string]any)
	e["inclusionProof"].(map[string]any)["rootHash"] = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	in.Bundle, _ = json.Marshal(b)
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrTlog)
}

func TestVerifyUntrustedSigstore(t *testing.T) {
	f := newFixture(t)
	other := releaseverifytest.MustNew()
	otherRoot, err := other.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.TrustedMaterial = otherRoot
	_, err = rv.Verify(in)
	if err == nil || (!errors.Is(err, rv.ErrTlog) && !errors.Is(err, rv.ErrSignature)) {
		t.Fatalf("err = %v; want a signature or tlog failure", err)
	}
}

// The production default: a bundle from the fake instance against the embedded
// public-good root must fail.
func TestVerifyEmbeddedRootRejectsFake(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.TrustedMaterial = nil
	if _, err := rv.Verify(in); err == nil {
		t.Fatal("fake Sigstore accepted by the public-good root")
	}
}

func TestVerifyLegacyReleaseWithoutBundle(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Bundle = nil
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrSignature)
	if !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("legacy message missing: %v", err)
	}
}

func TestVerifyBadTag(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Tag = "v1.5.0/../x"
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrManifest)
}

func editManifest(t *testing.T, f *fixture, mut func(m map[string]any)) rv.Input {
	t.Helper()
	mut(f.rel.Manifest)
	if err := f.rel.Rebuild(f.ss, releaseverifytest.GitHubRelease(repo, tag)); err != nil {
		t.Fatal(err)
	}
	return f.input()
}

func TestManifestVersionMismatch(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(editManifest(t, f, func(m map[string]any) { m["version"] = "1.4.0" }))
	wantKind(t, err, rv.ErrManifest)
}

func TestManifestTagMismatch(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(editManifest(t, f, func(m map[string]any) { m["tag"] = "v1.4.0" }))
	wantKind(t, err, rv.ErrManifest)
}

func TestManifestBadPlatformDigest(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(editManifest(t, f, func(m map[string]any) {
		a := m["assets"].(map[string]any)[platform].(map[string]any)
		a["sha256"] = strings.Repeat("1", 64)
	}))
	wantKind(t, err, rv.ErrManifest)
}

func TestManifestNotMatchingChecksums(t *testing.T) {
	f := newFixture(t)
	in := f.input()
	in.Manifest = append([]byte(" "), in.Manifest...) // valid JSON, other digest
	_, err := rv.Verify(in)
	wantKind(t, err, rv.ErrChecksum)
}

func TestConstraintsWithoutHashes(t *testing.T) {
	f := newFixture(t)
	f.rel.Files[f.rel.Cons] = []byte("anyio==4.4.0 \\\n    --hash=sha256:" + strings.Repeat("ab", 32) + "\nrequests==2.32.0\n")
	p := f.rel.Manifest["pipeline"].(map[string]any)
	p["constraints_sha256"] = sha(f.rel.Files[f.rel.Cons])
	_, err := rv.Verify(editManifest(t, f, func(map[string]any) {}))
	wantKind(t, err, rv.ErrNoHashes)
	if !strings.Contains(err.Error(), "requests==2.32.0") {
		t.Fatalf("error does not name the package: %v", err)
	}
}

// Story 38.9: the cuda pin set (torch's CUDA build for Windows) is checked as the
// default one is: listed in checksums.txt with its digest, every pin hashed.
func TestTheCUDAConstraintsAreCheckedToo(t *testing.T) {
	cuda := "maktaba-pipeline-1.5.0-constraints-cuda.txt"
	hashed := []byte("--extra-index-url https://download.pytorch.org/whl/cu130\ntorch==2.14.1+cu130 ; sys_platform == 'win32' \\\n    --hash=sha256:" + strings.Repeat("ab", 32) + "\n")
	f := newFixture(t)
	f.rel.Files[cuda] = hashed
	in := editManifest(t, f, func(m map[string]any) {
		p := m["pipeline"].(map[string]any)
		p["constraints_cuda"], p["constraints_cuda_sha256"] = cuda, sha(hashed)
	})
	in.Artifacts = append(in.Artifacts, rv.Artifact{Name: cuda, Data: hashed})
	res, err := rv.Verify(in)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Manifest.Pipeline.ConstraintsCUDA != cuda || !res.Manifest.Pipeline.IsConstraints(cuda) {
		t.Fatalf("pipeline = %+v", res.Manifest.Pipeline)
	}

	f = newFixture(t)
	unhashed := []byte("torch==2.14.1+cu130\n")
	f.rel.Files[cuda] = unhashed
	in = editManifest(t, f, func(m map[string]any) {
		p := m["pipeline"].(map[string]any)
		p["constraints_cuda"], p["constraints_cuda_sha256"] = cuda, sha(unhashed)
	})
	in.Artifacts = append(in.Artifacts, rv.Artifact{Name: cuda, Data: unhashed})
	_, err = rv.Verify(in)
	wantKind(t, err, rv.ErrNoHashes)

	f = newFixture(t)
	f.rel.Files[cuda] = hashed
	_, err = rv.Verify(editManifest(t, f, func(m map[string]any) {
		p := m["pipeline"].(map[string]any)
		p["constraints_cuda"], p["constraints_cuda_sha256"] = cuda, strings.Repeat("1", 64)
	}))
	wantKind(t, err, rv.ErrManifest)
}

func TestCheckConstraints(t *testing.T) {
	ok := "# header\n--index-url https://pypi.org/simple\nfoo==1 --hash=sha256:" + strings.Repeat("a", 64) + "\n\nbar==2 \\\n  --hash=sha256:" + strings.Repeat("b", 64) + "  # via foo\n"
	if err := rv.CheckConstraints("c.txt", []byte(ok)); err != nil {
		t.Fatalf("CheckConstraints(ok): %v", err)
	}
	h := strings.Repeat("a", 64)
	for _, bad := range []string{"", "# only comments\n", "foo==1\n", "foo==1 \\\n  --hash=md5:abc\n",
		// pip reads "#" after any whitespace (tab too) as a comment: no hash.
		"foo==1\t#--hash=sha256:" + h + "\n",
		"foo==1\t# --hash=sha256:" + h + "\n",
		"foo==1 \\\n#--hash=sha256:" + h + "\n",
	} {
		if err := rv.CheckConstraints("c.txt", []byte(bad)); !errors.Is(err, rv.ErrNoHashes) {
			t.Errorf("CheckConstraints(%q) = %v, want ErrNoHashes", bad, err)
		}
	}
}

func TestIdentityValidation(t *testing.T) {
	for _, bad := range []string{"no-slash", "a/b/c", "../x", "owner/na me", "https://github.com/a/b"} {
		if _, err := rv.NewIdentity(bad); err == nil {
			t.Errorf("NewIdentity(%q) accepted", bad)
		}
	}
	id, err := rv.NewIdentity("")
	if err != nil || id.Repo() != rv.DefaultRepo {
		t.Fatalf("default identity = %v, %v", id.Repo(), err)
	}
	if got, want := id.SAN("v2.0.0"), "https://github.com/Hamza-Labs-Core/Maktaba/.github/workflows/release.yml@refs/tags/v2.0.0"; got != want {
		t.Fatalf("SAN = %q, want %q", got, want)
	}
	t.Setenv(rv.RepoEnv, "someone/Maktaba")
	if id, _ := rv.IdentityFromEnv(); id.Repo() != "someone/Maktaba" {
		t.Fatalf("IdentityFromEnv = %q", id.Repo())
	}
}

func TestExtractBinary(t *testing.T) {
	ss := releaseverifytest.MustNew()
	for _, p := range []string{"linux/amd64", "windows/amd64"} {
		rel := ss.MustRelease(repo, tag, p)
		entry, b, err := rv.ExtractBinary(rel.Archive, rel.Files[rel.Archive], "maktaba-server")
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if string(b) != string(rel.Binaries[entry]) {
			t.Fatalf("%s: extracted %q = %q", p, entry, b)
		}
	}
	if _, _, err := rv.ExtractBinary("x.rar", nil, "maktaba-server"); err == nil {
		t.Fatal("unsupported archive accepted")
	}
}

// Repo-jacking: the organisation renamed or deleted and its name registered
// again by someone else, whose own Maktaba repository's release workflow signs
// with the very same SAN, repository URI, ref and trigger. Only GitHub's
// numeric ids tell the two apart.
func TestVerifyReRegisteredRepoName(t *testing.T) {
	for name, edit := range map[string]func(*releaseverifytest.SignOptions){
		"repository id": func(o *releaseverifytest.SignOptions) { o.RepositoryID = "999" },
		"owner id":      func(o *releaseverifytest.SignOptions) { o.OwnerID = "999" },
		"owner uri":     func(o *releaseverifytest.SignOptions) { o.OwnerURI = "https://github.com/someone-else" },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			_, err := rv.Verify(resigned(t, f, edit))
			wantKind(t, err, rv.ErrIdentity)
		})
	}
}

// release.yml reached through another workflow (a workflow_call from a
// workflow the tag push started) carries release.yml in the SAN but the
// caller in BuildConfigURI.
func TestVerifyCalledFromOtherWorkflow(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) {
		o.BuildConfigURI = "https://github.com/" + repo + "/.github/workflows/ci.yml@refs/tags/" + tag
	}))
	wantKind(t, err, rv.ErrIdentity)
}

func TestVerifySelfHostedRunner(t *testing.T) {
	f := newFixture(t)
	_, err := rv.Verify(resigned(t, f, func(o *releaseverifytest.SignOptions) { o.RunnerEnvironment = "self-hosted" }))
	wantKind(t, err, rv.ErrIdentity)
}

// release.json must name the commit the signing run was built from.
func TestManifestCommitNotTheSignedOne(t *testing.T) {
	f := newFixture(t)
	f.rel.Manifest["commit"] = strings.Repeat("1b", 20)
	if err := f.rel.Rebuild(f.ss, releaseverifytest.GitHubRelease(repo, tag)); err != nil {
		t.Fatal(err)
	}
	_, err := rv.Verify(f.input())
	wantKind(t, err, rv.ErrManifest)
}

// A fork named by MAKTABA_UPDATE_REPO is pinned by name only (its ids are not
// known here), so its own releases still verify.
func TestForkIdentityNotPinnedByID(t *testing.T) {
	ss := releaseverifytest.MustNew()
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	rel := ss.MustRelease("someone/Maktaba", tag, platform)
	id, err := rv.NewIdentity("someone/Maktaba")
	if err != nil {
		t.Fatal(err)
	}
	_, err = rv.Verify(rv.Input{Tag: tag, Identity: id, TrustedMaterial: tm,
		Checksums: rel.Files["checksums.txt"], Bundle: rel.Files["checksums.txt.sigstore.json"], Manifest: rel.Files["release.json"]})
	if err != nil {
		t.Fatalf("fork release: %v", err)
	}
}
