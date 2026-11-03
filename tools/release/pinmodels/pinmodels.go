// Package pinmodels pins the catalog's models (Epic 51, story 51.15): each Hugging Face
// source to a commit sha with every file's sha256 and size, each ollama source to its
// registry manifest digest with every layer's. tools/release/cmd/pin-models runs it in a
// normal pull request; the release only copies the pins into the signed index.
//
// Hugging Face's API gives a large (LFS) file's sha256 (lfs.oid); a small file has only a
// git blob id there, so it is downloaded (at most MaxSmallFile) and hashed. A gated
// repo's small files need the owner's token (HF_TOKEN); without it only its LFS files are
// pinned, and the result says so.
package pinmodels

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/Hamza-Labs-Core/Maktaba/shared/components"
)

// MaxSmallFile is the largest non-LFS file downloaded to hash.
const MaxSmallFile = 16 << 20

// Client reaches Hugging Face and the ollama registry.
type Client struct {
	HTTP     *http.Client
	HF       string // https://huggingface.co
	Registry string // https://registry.ollama.ai
	Token    string // HF_TOKEN, for a gated repo's small files ("" none)
}

// File is one pinned file.
type File = components.File

// Pin is one source pinned.
type Pin struct {
	Source  string // the pinned source (hf:<repo>@<sha> or ollama:<name>:<tag>@sha256:<hex>)
	Gated   bool
	Files   []File
	Skipped []string // files left out: a gated repo's small files without a token
}

var (
	hfRe     = regexp.MustCompile(`^hf:([A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*)(?:@([0-9a-f]{40}))?$`)
	ollamaRe = regexp.MustCompile(`^ollama:([a-z0-9][a-z0-9._/-]*):([A-Za-z0-9._-]+)(?:@sha256:([0-9a-f]{64}))?$`)
)

// Rules are the files a repo's component needs, as the pipeline fetches them
// (pipeline/src/maktaba_pipeline/models/fetch_cli.py): Include globs (none: every file),
// then Exclude globs.
type Rules struct{ Include, Exclude []string }

// ct2Files are stt/engine.py's CT2_FILES: what faster-whisper loads.
var ct2Files = []string{"config.json", "preprocessor_config.json", "model.bin", "tokenizer.json", "vocabulary.*"}

// docs are never needed.
var docs = []string{".gitattributes", "*.md", "LICENSE*", "*.png", "*.jpg"}

// RulesFor is the file rules of a Hugging Face repo.
func RulesFor(repo string) Rules {
	switch {
	case strings.HasPrefix(repo, "Systran/faster-whisper-"), strings.HasPrefix(repo, "dropbox-dash/faster-whisper-"):
		return Rules{Include: ct2Files}
	case strings.HasPrefix(repo, "CohereLabs/"):
		// stt/cohere_cuda.py ALLOW_PATTERNS (mlx-audio reads the same files)
		return Rules{Include: []string{"*.json", "*.safetensors", "*.model"}}
	case repo == "intfloat/multilingual-e5-small":
		// fetch_cli.py _EMBEDDER_IGNORE: other frameworks' weights
		return Rules{Exclude: append([]string{"*.onnx", "onnx/*", "openvino/*", "*.msgpack", "*.h5", "*.ot", "*.tflite"}, docs...)}
	}
	return Rules{Exclude: docs}
}

func (r Rules) keep(p string) bool {
	match := func(globs []string) bool {
		for _, g := range globs {
			if ok, _ := path.Match(g, p); ok {
				return true
			}
			if ok, _ := path.Match(g, path.Base(p)); ok && !strings.Contains(g, "/") {
				return true
			}
		}
		return false
	}
	if len(r.Include) > 0 && !match(r.Include) {
		return false
	}
	return !match(r.Exclude)
}

// PinSource pins one source. A source pinned already keeps its revision unless update;
// its files are listed again from that revision. known are the files the catalog holds
// for it now: a gated repo's API hides digests from a caller without a token, and those
// are then kept as they are (when the size still matches), never guessed.
func (c *Client) PinSource(ctx context.Context, source string, update bool, known []File) (*Pin, error) {
	if m := hfRe.FindStringSubmatch(source); m != nil {
		return c.pinHF(ctx, m[1], m[2], update, known)
	}
	if m := ollamaRe.FindStringSubmatch(source); m != nil {
		return c.pinOllama(ctx, m[1], m[2], m[3], update, known)
	}
	return nil, fmt.Errorf("pin-models: %q is not an hf: or ollama: source", source)
}

func (c *Client) get(ctx context.Context, u string, header map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, &statusError{url: u, code: resp.StatusCode}
	}
	return resp, nil
}

type statusError struct {
	url  string
	code int
}

func (e *statusError) Error() string { return fmt.Sprintf("GET %s: HTTP %d", e.url, e.code) }

func (c *Client) getJSON(ctx context.Context, u string, header map[string]string, v any) (http.Header, error) {
	resp, err := c.get(ctx, u, header)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(v); err != nil {
		return nil, fmt.Errorf("GET %s: %w", u, err)
	}
	return resp.Header, nil
}

func (c *Client) auth() map[string]string {
	if c.Token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + c.Token}
}

type hfInfo struct {
	SHA   string `json:"sha"`
	Gated any    `json:"gated"` // false, "auto" or "manual"
}

type hfEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

var hexRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

var nextLink = regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)

func (c *Client) pinHF(ctx context.Context, repo, rev string, update bool, known []File) (*Pin, error) {
	want := rev
	if want == "" || update {
		want = "main"
	}
	var info hfInfo
	if _, err := c.getJSON(ctx, c.HF+"/api/models/"+repo+"/revision/"+url.PathEscape(want), c.auth(), &info); err != nil {
		return nil, err
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(info.SHA) {
		return nil, fmt.Errorf("pin-models: %s@%s: no commit sha in the answer", repo, want)
	}
	gated := info.Gated != nil && info.Gated != false
	p := &Pin{Source: "hf:" + repo + "@" + info.SHA, Gated: gated}
	kept := map[string]File{}
	if rev == info.SHA {
		for _, f := range known {
			kept[f.Path] = f
		}
	}
	// keepKnown takes the catalog's pin of a file the API will not show
	keepKnown := func(e hfEntry, size int64) bool {
		if f, ok := kept[e.Path]; ok && f.Size == size {
			f.Source = ""
			p.Files = append(p.Files, f)
			return true
		}
		p.Skipped = append(p.Skipped, e.Path)
		return false
	}
	rules := RulesFor(repo)
	next := c.HF + "/api/models/" + repo + "/tree/" + info.SHA + "?recursive=true"
	for next != "" {
		var entries []hfEntry
		h, err := c.getJSON(ctx, next, c.auth(), &entries)
		if err != nil {
			return nil, err
		}
		next = ""
		if m := nextLink.FindStringSubmatch(h.Get("Link")); m != nil {
			next = m[1]
		}
		for _, e := range entries {
			if e.Type != "file" || !rules.keep(e.Path) {
				continue
			}
			if e.LFS != nil {
				if hexRe.MatchString(e.LFS.OID) {
					p.Files = append(p.Files, File{Path: e.Path, SHA256: e.LFS.OID, Size: e.LFS.Size})
				} else if !gated {
					return nil, fmt.Errorf("pin-models: %s/%s: the API gives no sha256", repo, e.Path)
				} else {
					keepKnown(e, e.LFS.Size)
				}
				continue
			}
			sum, n, err := c.hashSmall(ctx, repo, info.SHA, e.Path)
			var se *statusError
			if gated && errors.As(err, &se) && (se.code == http.StatusUnauthorized || se.code == http.StatusForbidden) {
				keepKnown(e, e.Size)
				continue
			}
			if err != nil {
				return nil, err
			}
			if n != e.Size {
				return nil, fmt.Errorf("pin-models: %s/%s: %d bytes, the API says %d", repo, e.Path, n, e.Size)
			}
			p.Files = append(p.Files, File{Path: e.Path, SHA256: sum, Size: n})
		}
	}
	if len(p.Files) == 0 {
		return nil, fmt.Errorf("pin-models: %s@%s: no files kept", repo, info.SHA)
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	return p, nil
}

func (c *Client) hashSmall(ctx context.Context, repo, sha, file string) (string, int64, error) {
	resp, err := c.get(ctx, c.HF+"/"+repo+"/resolve/"+sha+"/"+escapePath(file), c.auth())
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(resp.Body, MaxSmallFile+1))
	if err != nil {
		return "", 0, err
	}
	if n > MaxSmallFile {
		return "", 0, fmt.Errorf("pin-models: %s/%s is not an LFS file but is over %d bytes", repo, file, MaxSmallFile)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

type ollamaManifest struct {
	Config ollamaLayer   `json:"config"`
	Layers []ollamaLayer `json:"layers"`
}

type ollamaLayer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

// pinOllama: the registry serves a manifest by its tag only, so a pinned source whose tag
// has moved keeps the catalog's pin (update moves it).
func (c *Client) pinOllama(ctx context.Context, name, tag, digest string, update bool, known []File) (*Pin, error) {
	repo := name
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	resp, err := c.get(ctx, c.Registry+"/v2/"+repo+"/manifests/"+url.PathEscape(tag),
		map[string]string{"Accept": "application/vnd.docker.distribution.manifest.v2+json"})
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if digest != "" && !update && got != digest {
		if len(known) == 0 {
			return nil, fmt.Errorf("pin-models: ollama %s:%s: the tag is at %s now, pinned %s with no files: run with --update", name, tag, got, digest)
		}
		return &Pin{Source: "ollama:" + name + ":" + tag + "@sha256:" + digest, Files: known,
			Skipped: []string{"the tag moved to sha256:" + got + "; the pin is kept (--update moves it)"}}, nil
	}
	var m ollamaManifest
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("pin-models: ollama %s:%s: %w", name, tag, err)
	}
	p := &Pin{Source: "ollama:" + name + ":" + tag + "@sha256:" + got}
	for _, l := range append([]ollamaLayer{m.Config}, m.Layers...) {
		hexd, ok := strings.CutPrefix(l.Digest, "sha256:")
		if !ok || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(hexd) || l.Size <= 0 {
			return nil, fmt.Errorf("pin-models: ollama %s:%s: bad layer %q", name, tag, l.Digest)
		}
		// ollama keeps each blob as blobs/sha256-<hex>
		p.Files = append(p.Files, File{Path: "blobs/sha256-" + hexd, SHA256: hexd, Size: l.Size})
	}
	sort.Slice(p.Files, func(i, j int) bool { return p.Files[i].Path < p.Files[j].Path })
	return p, nil
}

// Result is one component's pins.
type Result struct {
	ID      string
	Source  string            // the default source, pinned
	Sources map[string]string // per platform, pinned
	Gated   bool
	Files   []File
	Skipped []string
}

// PinCatalog pins every model source of every asset in the catalog file's bytes and
// returns the file rewritten, with what it did. A source that is not a model's is left.
func (c *Client) PinCatalog(ctx context.Context, data []byte, update bool) ([]byte, []Result, error) {
	ids, err := assetIDs(data)
	if err != nil {
		return nil, nil, err
	}
	out := data
	var results []Result
	for _, id := range ids {
		block, ok := findBlock(out, id)
		if !ok {
			continue
		}
		src, srcs := blockSources(out[block.start:block.end])
		if !components.IsModelSource(src) && !anyModel(srcs) {
			continue
		}
		r := Result{ID: id, Source: src, Sources: map[string]string{}}
		pinned := map[string]*Pin{}
		known, err := blockFiles(out[block.start:block.end])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", id, err)
		}
		pin := func(s string) (string, error) {
			if !components.IsModelSource(s) {
				return s, nil
			}
			if p, ok := pinned[s]; ok {
				return p.Source, nil
			}
			var have []File
			for _, f := range known {
				if f.Source == s || (f.Source == "" && s == src) {
					have = append(have, f)
				}
			}
			p, err := c.PinSource(ctx, s, update, have)
			if err != nil {
				return "", fmt.Errorf("%s: %w", id, err)
			}
			pinned[s] = p
			return p.Source, nil
		}
		if r.Source, err = pin(src); err != nil {
			return nil, nil, err
		}
		for _, plat := range sortedKeys(srcs) {
			if r.Sources[plat], err = pin(srcs[plat]); err != nil {
				return nil, nil, err
			}
		}
		done := map[string]bool{}
		for _, s := range append([]string{src}, mapValues(srcs)...) {
			p := pinned[s]
			if p == nil || done[s] {
				continue
			}
			done[s] = true
			r.Gated = r.Gated || p.Gated
			for _, f := range p.Files {
				if p.Source != r.Source {
					f.Source = p.Source
				}
				r.Files = append(r.Files, f)
			}
			r.Skipped = append(r.Skipped, p.Skipped...)
		}
		out = rewriteBlock(out, block, r)
		results = append(results, r)
	}
	if _, err := components.Load(out); err != nil {
		return nil, nil, fmt.Errorf("pin-models: the pinned catalog does not load: %w", err)
	}
	return out, results, nil
}

func anyModel(m map[string]string) bool {
	for _, s := range m {
		if components.IsModelSource(s) {
			return true
		}
	}
	return false
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assetIDs are the ids of the catalog's [[component]] blocks of kind asset, read from the
// text (the file may not load yet: it is what is being fixed).
func assetIDs(data []byte) ([]string, error) {
	var ids []string
	for _, b := range blocks(data) {
		text := string(data[b.start:b.end])
		if m := kindRe.FindStringSubmatch(text); m != nil && m[1] == components.KindAsset {
			if id := idRe.FindStringSubmatch(text); id != nil {
				ids = append(ids, id[1])
			}
		}
	}
	return ids, nil
}

type span struct{ start, end int }

var (
	headerRe  = regexp.MustCompile(`(?m)^\[\[component\]\]\s*$`)
	idRe      = regexp.MustCompile(`(?m)^id\s*=\s*"([^"]+)"`)
	kindRe    = regexp.MustCompile(`(?m)^kind\s*=\s*"([^"]+)"`)
	sourceRe  = regexp.MustCompile(`(?m)^source(\s*)=\s*"([^"]*)"\s*$`)
	sourcesRe = regexp.MustCompile(`(?m)^sources(\s*)=\s*\{([^}\n]*)\}\s*$`)
	pairRe    = regexp.MustCompile(`([a-z0-9-]+)\s*=\s*"([^"]*)"`)
	gatedRe   = regexp.MustCompile(`(?m)^gated\s*=.*\n`)
	filesRe   = regexp.MustCompile(`(?ms)^files\s*=\s*\[.*?^\]\s*\n`)
	payloadRe = regexp.MustCompile(`(?m)^in_payload\s*=.*\n`)
)

// blocks are the [[component]] blocks: from a header to the next header or the end, less
// the comment lines that lead the next block.
func blocks(data []byte) []span {
	idx := headerRe.FindAllIndex(data, -1)
	var out []span
	for i, h := range idx {
		end := len(data)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		out = append(out, span{h[0], end})
	}
	return out
}

func findBlock(data []byte, id string) (span, bool) {
	for _, b := range blocks(data) {
		if m := idRe.FindSubmatch(data[b.start:b.end]); m != nil && string(m[1]) == id {
			return b, true
		}
	}
	return span{}, false
}

// blockFiles are the files a block lists now.
func blockFiles(block []byte) ([]File, error) {
	var doc struct {
		Component []struct {
			Files []File `toml:"files"`
		} `toml:"component"`
	}
	if _, err := toml.Decode(string(block), &doc); err != nil {
		return nil, err
	}
	if len(doc.Component) != 1 {
		return nil, fmt.Errorf("pin-models: not one component block")
	}
	return doc.Component[0].Files, nil
}

func blockSources(block []byte) (string, map[string]string) {
	src := ""
	if m := sourceRe.FindSubmatch(block); m != nil {
		src = string(m[2])
	}
	srcs := map[string]string{}
	if m := sourcesRe.FindSubmatch(block); m != nil {
		for _, p := range pairRe.FindAllSubmatch(m[2], -1) {
			srcs[string(p[1])] = string(p[2])
		}
	}
	return src, srcs
}

// rewriteBlock writes r's source, sources, gated and files into the block.
func rewriteBlock(data []byte, b span, r Result) []byte {
	block := string(data[b.start:b.end])
	block = sourceRe.ReplaceAllString(block, "source${1}= "+quote(r.Source))
	if len(r.Sources) > 0 {
		var pairs []string
		for _, k := range sortedKeys(r.Sources) {
			pairs = append(pairs, k+" = "+quote(r.Sources[k]))
		}
		block = sourcesRe.ReplaceAllString(block, "sources${1}= { "+strings.Join(pairs, ", ")+" }")
	}
	block = gatedRe.ReplaceAllString(block, "")
	block = filesRe.ReplaceAllString(block, "")
	var add bytes.Buffer
	if r.Gated {
		add.WriteString("gated      = true\n")
	}
	add.WriteString("# pinned by tools/release/cmd/pin-models\nfiles      = [\n")
	for _, f := range r.Files {
		add.WriteString("  { ")
		if f.Source != "" {
			fmt.Fprintf(&add, "source = %s, ", quote(f.Source))
		}
		fmt.Fprintf(&add, "path = %s, sha256 = %s, size = %d },\n", quote(f.Path), quote(f.SHA256), f.Size)
	}
	add.WriteString("]\n")
	block = strings.Replace(block, "# pinned by tools/release/cmd/pin-models\n", "", 1)
	if loc := payloadRe.FindStringIndex(block); loc != nil {
		block = block[:loc[1]] + add.String() + block[loc[1]:]
	} else {
		block = strings.TrimRight(block, "\n") + "\n" + add.String() + "\n"
	}
	var out bytes.Buffer
	out.Write(data[:b.start])
	out.WriteString(block)
	out.Write(data[b.end:])
	return out.Bytes()
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Summary is a human line per result.
func Summary(results []Result) string {
	var b strings.Builder
	for _, r := range results {
		var size int64
		for _, f := range r.Files {
			size += f.Size
		}
		fmt.Fprintf(&b, "%s: %s, %d files, %.1f MB", r.ID, r.Source, len(r.Files), float64(size)/1e6)
		if len(r.Skipped) > 0 {
			fmt.Fprintf(&b, " (gated: %d small files left out without HF_TOKEN: %s)", len(r.Skipped), strings.Join(r.Skipped, ", "))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
