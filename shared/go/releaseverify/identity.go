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
	// RunnerEnvironment: the release is built on GitHub-hosted runners only; a
	// self-hosted runner registered to the repository cannot sign one.
	RunnerEnvironment = "github-hosted"
	// DefaultRepoID and DefaultOwnerID are GitHub's immutable numeric ids of
	// DefaultRepo and its owner, which Fulcio records next to the names. Names
	// can be freed and re-registered (an organisation renamed or deleted); the
	// ids cannot, so a look-alike repository under the old name fails.
	DefaultRepoID  = "1229315126"
	DefaultOwnerID = "247904019"
	// RepoEnv names the startup setting that retargets a fork (Epic 28.2). It
	// changes the repository and the expected signing identity together; no API
	// field can change either (story 35.1 AC7).
	RepoEnv = "MAKTABA_UPDATE_REPO"
)

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})/[A-Za-z0-9._-]{1,100}$`)
)

// Identity is the pinned signer of a release: the release workflow of one
// repository, run by a tag push, for exactly the tag being installed.
type Identity struct {
	repo string
	// repoID and ownerID pin the repository by GitHub's numeric ids; set for
	// DefaultRepo (a fork named by MAKTABA_UPDATE_REPO is pinned by name).
	repoID, ownerID string
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
	if repo == DefaultRepo {
		return Identity{repo: repo, repoID: DefaultRepoID, ownerID: DefaultOwnerID}, nil
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

// ids are the pinned numeric ids; the zero Identity is DefaultRepo's.
func (id Identity) ids() (repoID, ownerID string) {
	if id.repo == "" {
		return DefaultRepoID, DefaultOwnerID
	}
	return id.repoID, id.ownerID
}

// RepoURI is the repository URL as Fulcio records it.
func (id Identity) RepoURI() string { return "https://github.com/" + id.Repo() }

// SAN is the exact certificate subject the release workflow gets for tag.
func (id Identity) SAN(tag string) string {
	return id.RepoURI() + "/" + WorkflowPath + "@refs/tags/" + tag
}

func (id Identity) certificateIdentity(tag string) (verify.CertificateIdentity, error) {
	san, err := verify.NewSANMatcher(id.SAN(tag), "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	iss, err := verify.NewIssuerMatcher(Issuer, "")
	if err != nil {
		return verify.CertificateIdentity{}, err
	}
	repoID, ownerID := id.ids()
	owner, _, _ := strings.Cut(id.Repo(), "/")
	return verify.NewCertificateIdentity(san, iss, certificate.Extensions{
		SourceRepositoryURI:             id.RepoURI(),
		SourceRepositoryRef:             "refs/tags/" + tag,
		SourceRepositoryOwnerURI:        "https://github.com/" + owner,
		SourceRepositoryIdentifier:      repoID,
		SourceRepositoryOwnerIdentifier: ownerID,
		// The SAN names the workflow that ran the signing job; BuildConfigURI
		// the one the tag push started. Both must be release.yml at this tag,
		// so no other workflow can reach the signing job (workflow_call).
		BuildConfigURI:    id.SAN(tag),
		BuildTrigger:      BuildTrigger,
		RunnerEnvironment: RunnerEnvironment,
	})
}

// The release feed's signer (feed.go): update-feed.yml of the pinned repository, on main,
// started by a release run's end (workflow_run), by hand (workflow_dispatch, a release
// pulled) or by its weekly schedule. A branch, a fork, a pull request or another workflow
// cannot sign a feed a machine reads.
const (
	FeedWorkflowPath = ".github/workflows/update-feed.yml"
	FeedRef          = "refs/heads/main"
)

// FeedTriggers are the events whose update-feed.yml run may sign a feed.
var FeedTriggers = []string{"workflow_run", "workflow_dispatch", "schedule"}

// FeedSAN is the exact certificate subject update-feed.yml on main gets.
func (id Identity) FeedSAN() string {
	return id.RepoURI() + "/" + FeedWorkflowPath + "@" + FeedRef
}

// feedIdentities are the certificate identities a feed's bundle may carry: FeedSAN under
// each of FeedTriggers, with the repository pinned as for a release.
func (id Identity) feedIdentities() ([]verify.CertificateIdentity, error) {
	san, err := verify.NewSANMatcher(id.FeedSAN(), "")
	if err != nil {
		return nil, err
	}
	iss, err := verify.NewIssuerMatcher(Issuer, "")
	if err != nil {
		return nil, err
	}
	repoID, ownerID := id.ids()
	owner, _, _ := strings.Cut(id.Repo(), "/")
	out := make([]verify.CertificateIdentity, 0, len(FeedTriggers))
	for _, trigger := range FeedTriggers {
		ci, err := verify.NewCertificateIdentity(san, iss, certificate.Extensions{
			SourceRepositoryURI:             id.RepoURI(),
			SourceRepositoryRef:             FeedRef,
			SourceRepositoryOwnerURI:        "https://github.com/" + owner,
			SourceRepositoryIdentifier:      repoID,
			SourceRepositoryOwnerIdentifier: ownerID,
			BuildConfigURI:                  id.FeedSAN(),
			BuildTrigger:                    trigger,
			RunnerEnvironment:               RunnerEnvironment,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, nil
}
