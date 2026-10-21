package devmigrate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Item is one file or folder the app keeps in its own place.
type Item struct {
	What string
	From string
	To   string
	Key  bool // a key file: an existing different one at To is an error, never replaced

	Size   int64 // bytes on disk (0 when From is missing)
	Exists bool
	Clone  bool // From and To are on one volume: an APFS clone takes no space
	Done   bool // To exists already (skipped)
}

// Items lists what moves for these settings into the app's state root and
// keys directory.
func Items(s Settings, state, keys string) []Item {
	hub := filepath.Join(state, "models", "huggingface", "hub")
	items := []Item{
		{What: "secrets key", From: s.SecretsKey, To: filepath.Join(keys, "secrets.key"), Key: true},
		{What: "stream signing key", From: s.StreamKey, To: filepath.Join(keys, "stream-signing.key"), Key: true},
		{What: "search index (Chroma)", From: s.ChromaDir, To: filepath.Join(state, "chroma")},
		{What: "streaming cache", From: s.CacheRoot, To: filepath.Join(state, "cache", "streaming")},
	}
	for _, repo := range s.HubRepos() {
		d := "models--" + strings.ReplaceAll(repo, "/", "--")
		items = append(items, Item{What: "model " + repo, From: filepath.Join(s.HFHome, "hub", d), To: filepath.Join(hub, d)})
	}
	items = append(items,
		// the Hugging Face sign-in (the second listen's model is gated)
		Item{What: "Hugging Face token", From: filepath.Join(s.HFHome, "token"), To: filepath.Join(state, "models", "huggingface", "token"), Key: true},
		Item{What: "CAMeL Tools data", From: s.CamelData, To: filepath.Join(state, "models", "camel_tools")},
		// the language model(s) of the LLM stages: the bundled Ollama's folder
		// (OLLAMA_MODELS of the ollama role), so the app does not download them again
		Item{What: "language models (Ollama)", From: s.OllamaModels, To: filepath.Join(state, "models", "ollama")},
	)
	for i := range items {
		items[i].stat()
	}
	return items
}

func (it *Item) stat() {
	if _, err := os.Lstat(it.From); err != nil {
		return
	}
	it.Exists = true
	it.Size = diskSize(it.From)
	if _, err := os.Lstat(it.To); err == nil && !emptyDir(it.To) {
		it.Done = true
	}
	it.Clone = sameVolume(it.From, it.To)
}

// diskSize sums the sizes under p (symlinks count as themselves).
func diskSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// Copy copies it.From to it.To (a clone where the volume can), beside and
// renamed into place. A key already there must be the same; anything else
// there stays, unless replace (a move made again: the copy there is older
// than the database it now goes with).
func (it Item) Copy(replace bool) error {
	if !it.Exists {
		return nil
	}
	from := it.From
	if it.Key { // a linked key file is copied as the file, never as the link
		r, err := filepath.EvalSymlinks(from)
		if err != nil {
			return err
		}
		from = r
	}
	there := false
	if emptyDir(it.To) {
		// a folder the app made before anything was in it (the model server's
		// models folder): the copy takes its place
		if err := os.RemoveAll(it.To); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(it.To); err == nil {
		if it.Key {
			if !sameFile(from, it.To) {
				return fmt.Errorf("%s exists and differs from %s: move it away first (it is never replaced)", it.To, it.From)
			}
			return nil
		}
		if !replace {
			return nil
		}
		there = true
	}
	if err := os.MkdirAll(filepath.Dir(it.To), 0o700); err != nil {
		return err
	}
	part := it.To + ".partial"
	if err := os.RemoveAll(part); err != nil {
		return err
	}
	if err := cloneOrCopy(from, part); err != nil {
		_ = os.RemoveAll(part)
		return fmt.Errorf("copy %s: %w", it.From, err)
	}
	if there {
		old := it.To + ".old"
		if err := os.RemoveAll(old); err != nil {
			return err
		}
		if err := os.Rename(it.To, old); err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(old) }()
	}
	return os.Rename(part, it.To)
}

// emptyDir: p is a folder (not a link) with no file anywhere in it (the model
// server makes its blobs/ and manifests/ folders when it starts).
func emptyDir(p string) bool {
	info, err := os.Lstat(p)
	if err != nil || !info.IsDir() {
		return false
	}
	empty := true
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			empty = false
			return filepath.SkipAll
		}
		return nil
	})
	return empty
}

func sameFile(a, b string) bool {
	x, err1 := os.ReadFile(a)
	y, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(x, y)
}

// copyTree copies a file or folder; symlinks are copied as links (the Hugging
// Face cache links snapshots to blobs inside the same repo folder).
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, dst)
		case info.IsDir():
			return os.MkdirAll(dst, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(p, dst, info.Mode().Perm())
		}
		return nil // sockets and the like stay behind
	})
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// existingParent is the nearest existing folder at or above p.
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil || errors.Is(err, fs.ErrPermission) {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return p
		}
		p = up
	}
}
