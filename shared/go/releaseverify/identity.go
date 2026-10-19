package releaseverify

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/sigstore/sigstore-go/pkg/fulcio/certificate"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	// DefaultRepo is the repository whose release workflow signs Maktaba releases.
	DefaultRepo = "Hamza-Labs-Core/Maktaba"
	// Issuer is the OIDC issuer of GitHub Actions workflow tokens.
	Issuer = "https://token.actions.githubusercontent.com"
	// WorkflowPath is the signing workflow inside the repository.
	WorkflowPath = ".github/workflows/release.yml"
	// BuildTrigger is the event that runs the release workflow (a tag push).
	BuildTrigger = "push"
	// RepoEnv names the startup setting that retargets a fork (Epic 28.2). It
	// changes the repository and the expected signing identity together; no API
	// field can change either (story 35.1 AC7).
	RepoEnv = "MAKTABA_UPDATE_REPO"
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
	// tagRe: vMAJOR.MINOR.PATCH with an optional prerelease suffix. Anything else
	// (refs with slashes, spaces, …) never reaches the SAN.
	tagRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)
)

// Identity is the pinned signer of a release: the release workflow of one
// repository, run by a tag push, for exactly the tag being installed.
type Identity struct {
	repo string
}

// NewIdentity returns the identity for repo ("owner/name"); "" means DefaultRepo.
func NewIdentity(repo string) (Identity, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		repo = DefaultRepo
	}
	if !repoRe.MatchString(repo) || strings.Contains(repo, "..") {
		return Identity{}, fmt.Errorf("releaseverify: invalid repository %q (want owner/name)", repo)
	}
	return Identity{repo: repo}, nil
}

// IdentityFromEnv is NewIdentity(os.Getenv(RepoEnv)).
func IdentityFromEnv() (Identity, error) { return NewIdentity(os.Getenv(RepoEnv)) }

// Repo is "owner/name".
func (id Identity) Repo() string {
	if id.repo == "" {
		return DefaultRepo
	}
	return id.repo
}

// RepoURI is the repository URL as Fulcio records it.
func (id Identity) RepoURI() string { return "https://github.com/" + id.Repo() }

// SAN is the exact certificate subject the release workflow gets for tag.
func (id Identity) SAN(tag string) string {
	return id.RepoURI() + "/" + WorkflowPath + "@refs/tags/" + tag
}

// ValidTag reports whether tag is a release tag Verify accepts.
func ValidTag(tag string) bool { return tagRe.MatchString(tag) }

func (id Identity) certificateIdentity(tag string) (verify.CertificateIdentity, error) {
	san, err := verify.NewSANMatcher(id.SAN(tag), "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	iss, err := verify.NewIssuerMatcher(Issuer, "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	return verify.NewCertificateIdentity(san, iss, certificate.Extensions{
		SourceRepositoryURI: id.RepoURI(),
		SourceRepositoryRef: "refs/tags/" + tag,
		BuildTrigger:        BuildTrigger,
	})
}
