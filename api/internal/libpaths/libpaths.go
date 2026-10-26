// Package libpaths resolves a library folder between the database's form and
// this machine's (epic 45).
//
// The database stores the NAS's paths (/Volumes/Originals/...) or canonical
// share paths (smb://nas/share/..., story 31.3). This machine may reach the
// same folder elsewhere (/Users/<me>/nas-nfs/...): the server hands every
// role its [media] path_map as MAKTABA_PATH_MAP. Every place the API touches
// a library folder on disk goes through ToLocal; the folder picker, which
// browses this machine's disk, hands paths back through ToDB.
package libpaths

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/cluster/agent"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/sharepath"
)

// EnvPathMap is the variable the server sets for every role.
const EnvPathMap = "MAKTABA_PATH_MAP"

var (
	mu      sync.Mutex
	rawSeen string
	parsed  agent.PathMap
	loaded  bool
)

// pathMap is the map in MAKTABA_PATH_MAP, parsed again only when the text
// changes (tests set the variable per case). A map that does not parse is
// empty: the paths stay as they are.
func pathMap() agent.PathMap {
	raw := os.Getenv(EnvPathMap)
	mu.Lock()
	defer mu.Unlock()
	if loaded && raw == rawSeen {
		return parsed
	}
	m, err := agent.ParsePathMap(raw)
	if err != nil {
		m = agent.PathMap{}
	}
	rawSeen, parsed, loaded = raw, m, true
	return parsed
}

// ToLocal maps a library folder (or file) as the database stores it to this
// machine's path: a canonical share path through the home's share map first,
// then the path map; anything else comes back unchanged.
func ToLocal(dbPath string) string {
	if p := sharepath.ToLocal(dbPath); p != dbPath {
		return p
	}
	m := pathMap()
	if out := m.ToLocal(dbPath); out != dbPath {
		return out
	}
	// a folder named without its trailing "/" ("/Volumes/Originals") is still
	// the map's prefix
	if !strings.HasSuffix(dbPath, "/") {
		if out := m.ToLocal(dbPath + "/"); out != dbPath+"/" {
			return strings.TrimSuffix(out, "/")
		}
	}
	return dbPath
}

// ToDB maps a path on this machine to the database's form through the path
// map; a path the map does not cover comes back unchanged.
func ToDB(localPath string) string {
	m := pathMap()
	p := filepath.ToSlash(localPath)
	if out := m.ToDB(p); out != p {
		return out
	}
	if !strings.HasSuffix(p, "/") {
		if out := m.ToDB(p + "/"); out != p+"/" {
			return strings.TrimSuffix(out, "/")
		}
	}
	return localPath
}

// Pair is one path map entry, without the trailing "/" the map keeps.
type Pair struct {
	DB    string
	Local string
}

// Pairs lists the path map's entries.
func Pairs() []Pair {
	var out []Pair
	for _, e := range pathMap().Pairs() {
		out = append(out, Pair{DB: trim(e[0]), Local: trim(e[1])})
	}
	return out
}

func trim(p string) string {
	if p == "/" {
		return p
	}
	return strings.TrimSuffix(p, "/")
}
