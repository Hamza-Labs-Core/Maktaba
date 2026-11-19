package fppackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// MaxMemberBytes is the largest file a wheel of a package may hold, the package's own
// unpacked limit (shared/go/componentpack PluginMaxUnpackedBytes, 54.01 §1).
const MaxMemberBytes = 200 << 20

// ReadWheel reads a wheel's RECORD (PEP 376 CSV: path, sha256=<urlsafe base64>, size) and
// checks every member against it: each member listed with its digest and size, each listed
// path a member. It answers each member's digest and bytes and the RECORD's own path. A member over the unpacked limit is refused,
// never hashed as a prefix.
func ReadWheel(b []byte) (map[string]string, map[string][]byte, string, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, nil, "", err
	}
	var record *zip.File
	files := map[string]*zip.File{}
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		if _, dup := files[f.Name]; dup {
			return nil, nil, "", fmt.Errorf("%s twice", f.Name)
		}
		files[f.Name] = f
		if strings.HasSuffix(f.Name, ".dist-info/RECORD") && strings.Count(f.Name, "/") == 1 {
			if record != nil {
				return nil, nil, "", fmt.Errorf("two RECORD files")
			}
			record = f
		}
	}
	if record == nil {
		return nil, nil, "", fmt.Errorf("no RECORD")
	}
	rc, err := record.Open()
	if err != nil {
		return nil, nil, "", err
	}
	rows, err := csv.NewReader(rc).ReadAll()
	_ = rc.Close()
	if err != nil {
		return nil, nil, "", fmt.Errorf("RECORD: %w", err)
	}
	out := map[string]string{}
	contents := map[string][]byte{}
	for _, r := range rows {
		if len(r) != 3 {
			return nil, nil, "", fmt.Errorf("RECORD: a row of %d fields", len(r))
		}
		path, digest := r[0], r[1]
		f, ok := files[path]
		if !ok {
			return nil, nil, "", fmt.Errorf("RECORD lists %s, which it does not hold", path)
		}
		if path == record.Name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, "", err
		}
		if f.UncompressedSize64 > MaxMemberBytes {
			_ = rc.Close()
			return nil, nil, "", fmt.Errorf("%s is over %d bytes", path, MaxMemberBytes)
		}
		data, err := io.ReadAll(io.LimitReader(rc, MaxMemberBytes+1))
		_ = rc.Close()
		if err != nil {
			return nil, nil, "", err
		}
		if len(data) > MaxMemberBytes {
			return nil, nil, "", fmt.Errorf("%s is over %d bytes", path, MaxMemberBytes)
		}
		sum := sha256.Sum256(data)
		size, err := strconv.ParseInt(r[2], 10, 64)
		if err != nil || size < 0 || size != int64(len(data)) || digest != "sha256="+base64.RawURLEncoding.EncodeToString(sum[:]) {
			return nil, nil, "", fmt.Errorf("%s does not match its RECORD", path)
		}
		out[path] = digest
		contents[path] = data
	}
	for name := range files {
		if _, ok := out[name]; !ok && name != record.Name {
			return nil, nil, "", fmt.Errorf("%s is not in RECORD", name)
		}
	}
	return out, contents, record.Name, nil
}

// compiled is a file Python writes for itself, which a wheel may or may not carry.
func compiled(p string) bool {
	return strings.Contains(p, "/__pycache__/") || strings.HasSuffix(p, ".pyc")
}

// CheckWheel: the plugin's wheel holds its own package (dist) and exactly its dist-info's
// METADATA, WHEEL and RECORD, each file as its RECORD says. Its METADATA names the plugin's
// distribution at this version and requires only maktaba-pipeline at this version, and its
// WHEEL says a pure py3-none-any wheel, so a package cannot pull another dependency in or
// claim another version. With the release's pipeline wheel's RECORD, its package's files
// equal that wheel's, digest for digest, both ways.
func CheckWheel(b []byte, dist, version string, pipeline map[string]string) error {
	rec, files, recPath, err := ReadWheel(b)
	if err != nil {
		return err
	}
	info := dist + "-" + version + ".dist-info/"
	if recPath != info+"RECORD" {
		return fmt.Errorf("its RECORD is %s, not %sRECORD", recPath, info)
	}
	own := 0
	for p, d := range rec {
		switch {
		case p == info+"METADATA" || p == info+"WHEEL":
		case strings.HasPrefix(p, dist+"/"):
			own++
			if pipeline != nil {
				if pd, ok := pipeline[p]; !ok || pd != d {
					return fmt.Errorf("%s is not the release's pipeline wheel's", p)
				}
			}
		default:
			return fmt.Errorf("%s is outside its package %s and its dist-info", p, dist)
		}
	}
	if own == 0 {
		return fmt.Errorf("it holds no file of %s", dist)
	}
	meta, ok := files[info+"METADATA"]
	if !ok {
		return fmt.Errorf("no %sMETADATA", info)
	}
	name := strings.ReplaceAll(dist, "_", "-")
	if err := checkHeaders(meta, map[string]string{"Name": name, "Version": version, "Requires-Dist": "maktaba-pipeline==" + version},
		"Metadata-Version", "Summary", "License", "Requires-Python"); err != nil {
		return fmt.Errorf("METADATA: %w", err)
	}
	wh, ok := files[info+"WHEEL"]
	if !ok {
		return fmt.Errorf("no %sWHEEL", info)
	}
	if err := checkHeaders(wh, map[string]string{"Root-Is-Purelib": "true", "Tag": "py3-none-any"}, "Wheel-Version", "Generator"); err != nil {
		return fmt.Errorf("WHEEL: %w", err)
	}
	for p := range pipeline {
		if strings.HasPrefix(p, dist+"/") && !compiled(p) {
			if _, ok := rec[p]; !ok {
				return fmt.Errorf("the release's pipeline wheel has %s, which it lacks", p)
			}
		}
	}
	return nil
}

// checkHeaders reads "Key: value" lines: each key of exact once with that value, the free
// keys at most once each with any value, and no other key or line.
func checkHeaders(b []byte, exact map[string]string, free ...string) error {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			return fmt.Errorf("the line %q", line)
		}
		if seen[k] {
			return fmt.Errorf("%s twice", k)
		}
		seen[k] = true
		if want, isExact := exact[k]; isExact {
			if v != want {
				return fmt.Errorf("%s is %q, not %q", k, v, want)
			}
			continue
		}
		allowed := false
		for _, f := range free {
			allowed = allowed || f == k
		}
		if !allowed {
			return fmt.Errorf("%s is not allowed", k)
		}
	}
	for k := range exact {
		if !seen[k] {
			return fmt.Errorf("no %s", k)
		}
	}
	return nil
}

// HasHeader: a "Key: value" line of b has that value.
func HasHeader(b []byte, key, value string) bool {
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(strings.TrimRight(line, "\r"), ": "); ok && k == key && v == value {
			return true
		}
	}
	return false
}
