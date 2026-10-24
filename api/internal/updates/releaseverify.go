package updates

import (
	"context"
	"errors"

	"github.com/sigstore/sigstore-go/pkg/root"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/releaseverify"
)

// TrustSource is what the release verifier needs from the update checker
// (system.Updater): the pinned signing identity of MAKTABA_UPDATE_REPO and
// the Sigstore trusted root it keeps fresh (story 35.1).
type TrustSource interface {
	Identity() (releaseverify.Identity, error)
	TrustedMaterial(ctx context.Context) (root.TrustedMaterial, error)
}

// ReleaseVerifier is the Verifier backed by shared/go/releaseverify — the one
// verifier the CLI, the API self-update and the catalog share.
type ReleaseVerifier struct{ Trust TrustSource }

// VerifyRelease runs releaseverify.Verify over the three signed files.
func (v ReleaseVerifier) VerifyRelease(ctx context.Context, tag string, f ReleaseFiles) (Verified, error) {
	if len(f.Manifest) == 0 || len(f.Bundle) == 0 {
		return Verified{}, &VerifyError{Kind: KindNoManifest, Detail: "release published before signed release.json"}
	}
	id, err := v.Trust.Identity()
	if err != nil {
		return Verified{}, &VerifyError{Kind: KindIdentity, Detail: err.Error()}
	}
	tm, err := v.Trust.TrustedMaterial(ctx)
	if err != nil {
		kind := KindUnavailable
		if errors.Is(err, releaseverify.ErrRootExpired) {
			kind = KindRootExpired
		}
		return Verified{}, &VerifyError{Kind: kind, Detail: err.Error()}
	}
	res, err := releaseverify.Verify(releaseverify.Input{Tag: tag, Identity: id,
		Checksums: f.Checksums, Bundle: f.Bundle, Manifest: f.Manifest, TrustedMaterial: tm})
	if err != nil {
		var e *releaseverify.Error
		if errors.As(err, &e) {
			return Verified{}, &VerifyError{Kind: kindOf(e.Kind), File: e.File, Detail: e.Detail}
		}
		return Verified{}, &VerifyError{Kind: KindUnavailable, Detail: err.Error()}
	}
	m := res.Manifest
	return Verified{
		Manifest: Manifest{V: m.V, Version: m.Version, Tag: m.Tag, Commit: m.Commit, Channel: m.Channel,
			Published: m.Published, SchemaRev: m.SchemaRev, MinNodeSchema: m.MinNodeSchema,
			BreaksPreviousHome: m.BreaksPreviousHome, MinUpgradeFrom: m.MinUpgradeFrom,
			EdgeProtocol: m.EdgeProtocol, LauncherProtocol: m.LauncherProtocol, AgentProtocol: m.AgentProtocol,
			AppPostgresMajor: m.AppPostgresMajor},
		ChecksumsSHA256: res.ChecksumsSHA256, SignedAt: res.SignedAt, Identity: res.Identity,
	}, nil
}

func kindOf(k error) string {
	for _, m := range []struct {
		err  error
		kind string
	}{
		{releaseverify.ErrSignature, KindSignature}, {releaseverify.ErrIdentity, KindIdentity},
		{releaseverify.ErrTlog, KindTlog}, {releaseverify.ErrChecksum, KindChecksum},
		{releaseverify.ErrManifest, KindManifest}, {releaseverify.ErrNoHashes, KindNoHashes},
		{releaseverify.ErrReplay, KindReplay}, {releaseverify.ErrDowngrade, KindDowngrade},
		{releaseverify.ErrRootExpired, KindRootExpired}, {releaseverify.ErrOlderBuild, KindOlderBuild},
	} {
		if errors.Is(k, m.err) {
			return m.kind
		}
	}
	return KindSignature
}
