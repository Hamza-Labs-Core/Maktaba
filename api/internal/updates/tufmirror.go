package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/httperror"
)

// The Sigstore trusted root reaches the machines through the home too (design §3.3):
//
//	GET /api/cluster/updates/tuf/{file}   a file of Sigstore's TUF repository
//
// The home passes on the files of the public repository (tuf-repo-cdn.sigstore.dev) and
// keeps those that never change (the versioned metadata, the targets named by their hash).
// A machine reads the repository from here when it cannot reach the public one, and
// verifies every file with TUF from the root it carries, exactly as it would the public
// one's: the home serves the trusted root, it is never trusted for it.

// DefaultTUFUpstream is Sigstore's public TUF repository (sigstore-go's tuf.DefaultMirror).
const DefaultTUFUpstream = "https://tuf-repo-cdn.sigstore.dev"

// tufFile is a name of the repository's: versioned metadata, the timestamp, or a target
// named by its SHA-256 (consistent snapshots).
var tufFile = regexp.MustCompile(`^(?:[0-9]+\.(?:root|snapshot|targets)\.json|timestamp\.json|targets/[0-9a-f]{64}\.[A-Za-z0-9._-]+)$`)

// tufTimestampTTL is how long the home passes on the timestamp it read last.
const tufTimestampTTL = 5 * time.Minute

// tufFileLimit bounds a repository file.
const tufFileLimit = 16 << 20

type tufCached struct {
	body []byte
	at   time.Time
}

func (m *Mirror) tufUpstream() string {
	if m.TUFUpstream != "" {
		return strings.TrimRight(m.TUFUpstream, "/")
	}
	return DefaultTUFUpstream
}

func (m *Mirror) serveTUF(w http.ResponseWriter, r *http.Request) {
	done, ok := m.machine(w, r)
	if !ok {
		return
	}
	defer done()
	name := chi.URLParam(r, "*")
	if !tufFile.MatchString(name) {
		httperror.Write(w, r, httperror.NotFound("not a file of the TUF repository"))
		return
	}
	body, code, err := m.tufGet(r.Context(), name)
	switch {
	case err != nil:
		m.log().Warn("update mirror: the TUF repository could not be read", "event", "update_mirror_failed",
			"file", name, "err", err.Error())
		httperror.Write(w, r, httperror.BadGateway(err.Error()))
	case code != http.StatusOK:
		// the TUF client looks for the next root until a 404: it must get one
		httperror.Write(w, r, &httperror.Error{Type: "about:blank#tuf", Title: http.StatusText(code), Status: code})
	default:
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	}
}

// tufGet is a repository file: kept (a file that never changes, or the timestamp read
// less than tufTimestampTTL ago), else read from the public repository. The public
// repository's own answer (a 404 among them) is passed on.
func (m *Mirror) tufGet(ctx context.Context, name string) ([]byte, int, error) {
	immutable := name != "timestamp.json"
	path := filepath.Join(m.Dir, "tuf", filepath.FromSlash(name))
	if immutable {
		if b, err := os.ReadFile(path); err == nil {
			if tufWhole(name, b) {
				return b, http.StatusOK, nil
			}
			_ = os.Remove(path) // a kept copy that is not whole: read again
		}
	} else {
		m.mu.Lock()
		c := m.tufTimestamp
		m.mu.Unlock()
		if c != nil && m.now().Sub(c.at) < tufTimestampTTL {
			return c.body, http.StatusOK, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.tufUpstream()+"/"+name, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := m.client().Do(req)
	if err != nil {
		if !immutable {
			if b := m.lastTimestamp(); b != nil {
				return b, http.StatusOK, nil // the machine's TUF client judges its expiry
			}
		}
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, tufFileLimit+1))
	if err != nil {
		return nil, 0, err
	}
	if len(b) > tufFileLimit {
		return nil, 0, fmt.Errorf("%s is larger than %d bytes", name, tufFileLimit)
	}
	if immutable {
		if tufWhole(name, b) && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			_ = writeSynced(path, b)
		}
	} else {
		m.mu.Lock()
		m.tufTimestamp = &tufCached{body: b, at: m.now()}
		m.mu.Unlock()
	}
	return b, http.StatusOK, nil
}

// tufWhole reports whether a repository file is whole, as far as the home can tell without
// TUF (the machine's TUF client verifies the rest): a target named by its SHA-256 has that
// digest, and metadata is JSON.
func tufWhole(name string, b []byte) bool {
	if rest, ok := strings.CutPrefix(name, "targets/"); ok {
		digest, _, _ := strings.Cut(rest, ".")
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]) == digest
	}
	return json.Valid(b)
}

func (m *Mirror) lastTimestamp() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tufTimestamp == nil {
		return nil
	}
	return m.tufTimestamp.body
}
