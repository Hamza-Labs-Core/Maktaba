package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The coverage map is CI's artifact `affected-tests-map` (ci.yml's coverage-map job, on
// each push to main). It is fetched with gh and kept in the git dir for a while.

const (
	mapArtifact = "affected-tests-map"
	mapFileName = "affected-tests-map.json"
	mapMaxAge   = 6 * time.Hour
)

var reRemote = regexp.MustCompile(`github\.com[:/]+([^/]+/[^/]+?)(?:\.git)?/?$`)

// repoSlug is owner/name from the origin remote.
func repoSlug(root string) (string, error) {
	out, err := git(root, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	m := reRemote.FindStringSubmatch(strings.TrimSpace(string(out)))
	if m == nil {
		return "", fmt.Errorf("origin %q is not a GitHub repository", strings.TrimSpace(string(out)))
	}
	return m[1], nil
}

func cacheDir(root string) (string, error) {
	out, err := git(root, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Join(dir, "affected-tests"), nil
}

// ghRunner runs gh and returns stdout (a seam for tests).
type ghRunner func(args ...string) ([]byte, error)

func realGH(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type artifactList struct {
	Artifacts []struct {
		ID          int64  `json:"id"`
		Expired     bool   `json:"expired"`
		CreatedAt   string `json:"created_at"`
		WorkflowRun struct {
			HeadSHA    string `json:"head_sha"`
			HeadBranch string `json:"head_branch"`
		} `json:"workflow_run"`
	} `json:"artifacts"`
}

// fetchMap downloads the newest map artifact built on main and returns its bytes.
func fetchMap(gh ghRunner, slug string) ([]byte, string, error) {
	out, err := gh("api", fmt.Sprintf("repos/%s/actions/artifacts?name=%s&per_page=20", slug, mapArtifact))
	if err != nil {
		return nil, "", err
	}
	var list artifactList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, "", fmt.Errorf("artifact list: %w", err)
	}
	for _, a := range list.Artifacts {
		if a.Expired || (a.WorkflowRun.HeadBranch != "" && a.WorkflowRun.HeadBranch != "main") {
			continue
		}
		zipped, err := gh("api", fmt.Sprintf("repos/%s/actions/artifacts/%d/zip", slug, a.ID))
		if err != nil {
			return nil, "", err
		}
		b, err := unzipFile(zipped, mapFileName)
		if err != nil {
			return nil, "", err
		}
		return b, fmt.Sprintf("main@%s (%s)", short(a.WorkflowRun.HeadSHA), a.CreatedAt), nil
	}
	return nil, "", fmt.Errorf("no %s artifact from main yet", mapArtifact)
}

func unzipFile(zipped []byte, name string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		return nil, fmt.Errorf("artifact zip: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, 512<<20))
	}
	return nil, fmt.Errorf("artifact has no %s", name)
}

func short(sha string) string {
	if len(sha) > 9 {
		return sha[:9]
	}
	return sha
}

// obtainMap finds a coverage map: a file given with --map, else the cache when fresh,
// else a fetch (kept in the cache), else a stale cache. Without one the tool works from
// its searches; the note says which.
func obtainMap(root, mapFlag string, fetch bool, gh ghRunner, now time.Time) (*CoverageMap, string) {
	switch mapFlag {
	case "none":
		return nil, "none (--map none): searches only"
	case "", "auto":
	default:
		m, err := loadMap(mapFlag)
		if err != nil {
			return nil, fmt.Sprintf("none (%v): searches only", err)
		}
		return m, mapFlag + describeMap(m)
	}
	dir, err := cacheDir(root)
	if err != nil {
		return nil, "none (" + err.Error() + "): searches only"
	}
	cached := filepath.Join(dir, mapFileName)
	noteFile := cached + ".from"
	var stale *CoverageMap
	staleNote := ""
	if st, err := os.Stat(cached); err == nil {
		if m, err := loadMap(cached); err == nil {
			from, _ := os.ReadFile(noteFile)
			note := strings.TrimSpace(string(from)) + describeMap(m)
			if now.Sub(st.ModTime()) < mapMaxAge || !fetch {
				return m, "cached " + note
			}
			stale, staleNote = m, "cached "+note+", could not refresh"
		}
	}
	if !fetch {
		return nil, "none (not fetched): searches only"
	}
	slug, err := repoSlug(root)
	if err == nil {
		var b []byte
		var from string
		b, from, err = fetchMap(gh, slug)
		if err == nil {
			var m *CoverageMap
			if m, err = parseMap(b); err == nil {
				if os.MkdirAll(dir, 0o755) == nil {
					_ = os.WriteFile(cached, b, 0o644)
					_ = os.WriteFile(noteFile, []byte(from), 0o644)
				}
				return m, "fetched " + from + describeMap(m)
			}
		}
	}
	if stale != nil {
		return stale, staleNote
	}
	return nil, fmt.Sprintf("none (%v): searches only", err)
}

func describeMap(m *CoverageMap) string {
	if m == nil || len(m.Sections) == 0 {
		return ""
	}
	return ", sections: " + strings.Join(m.sectionNames(), " ")
}
