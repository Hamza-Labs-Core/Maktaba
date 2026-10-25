package updates

import (
	"context"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify/releaseverifytest"
)

type fakeTrust struct {
	id releaseverify.Identity
	tm root.TrustedMaterial
}

func (f fakeTrust) Identity() (releaseverify.Identity, error) { return f.id, nil }
func (f fakeTrust) TrustedMaterial(context.Context) (root.TrustedMaterial, error) {
	return f.tm, nil
}

// The catalog's verifier is shared/go/releaseverify (story 35.1): a release
// signed by the pinned workflow verifies, another identity is an incident.
func TestReleaseVerifierAdapter(t *testing.T) {
	t.Parallel()
	ss := releaseverifytest.MustNew()
	tm, err := ss.TrustedRoot()
	if err != nil {
		t.Fatal(err)
	}
	id, _ := releaseverify.NewIdentity(releaseverify.DefaultRepo)
	v := ReleaseVerifier{Trust: fakeTrust{id: id, tm: tm}}
	files := func(r *releaseverifytest.FakeRelease) ReleaseFiles {
		return ReleaseFiles{Checksums: r.Files["checksums.txt"], Bundle: r.Files["checksums.txt.sigstore.json"], Manifest: r.Files["release.json"]}
	}

	good := ss.MustRelease(releaseverify.DefaultRepo, "v1.5.0", "linux/amd64")
	got, err := v.VerifyRelease(context.Background(), "v1.5.0", files(good))
	if err != nil {
		t.Fatal(err)
	}
	if got.Manifest.Version != "1.5.0" || got.ChecksumsSHA256 == "" || got.Identity == "" || got.Manifest.LauncherProtocol != 1 {
		t.Fatalf("verified = %+v", got)
	}

	fork := ss.MustRelease("someone/Maktaba", "v1.5.1", "linux/amd64")
	_, err = v.VerifyRelease(context.Background(), "v1.5.1", files(fork))
	ve := AsVerifyError(err)
	if ve == nil || !ve.Incident() {
		t.Fatalf("fork signature: %v", err)
	}

	_, err = v.VerifyRelease(context.Background(), "v1.4.0", ReleaseFiles{Checksums: []byte("x")})
	if ve := AsVerifyError(err); ve == nil || ve.Kind != KindNoManifest {
		t.Fatalf("pre-35.1 release: %v", err)
	}
}
