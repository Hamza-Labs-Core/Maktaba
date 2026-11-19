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
// path a member. It answers each member's digest, the bytes of its dist-info's own files
// (METADATA, WHEEL) and the RECORD's own path. A member over the unpacked limit is refused,
// never hashed as a prefix.
func ReadWheel(b []byte) (map[string]string, map[string][]byte, string, error) {
	z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return nil, nil, "", err
	}
	// the whole wheel too: a count and a total under limits, declared and read, so a small
	// file that expands into many members at the limit each cannot exhaust the runner
	if len(z.File) > maxWheelMembers {
		return nil, nil, "", fmt.Errorf("%d members, at most %d", len(z.File), maxWheelMembers)
	}
	var declared uint64
	for _, f := range z.File {
		declared += f.UncompressedSize64
	}
	if declared > maxWheelBytes {
		return nil, nil, "", fmt.Errorf("%d bytes unpacked, at most %d", declared, maxWheelBytes)
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
	// RECORD is a member too: over the unpacked limit, declared or read, it is refused
	if record.UncompressedSize64 > MaxMemberBytes {
		return nil, nil, "", fmt.Errorf("%s is over %d bytes", record.Name, MaxMemberBytes)
	}
	rc, err := record.Open()
	if err != nil {
		return nil, nil, "", err
	}
	recBytes, err := io.ReadAll(io.LimitReader(rc, MaxMemberBytes+1))
	_ = rc.Close()
	if err != nil {
		return nil, nil, "", err
	}
	if len(recBytes) > MaxMemberBytes {
		return nil, nil, "", fmt.Errorf("%s is over %d bytes", record.Name, MaxMemberBytes)
	}
	rows, err := csv.NewReader(bytes.NewReader(recBytes)).ReadAll()
	if err != nil {
		return nil, nil, "", fmt.Errorf("RECORD: %w", err)
	}
	out := map[string]string{}
	contents := map[string][]byte{}
	read := uint64(len(recBytes))
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
		if read += uint64(len(data)); read > maxWheelBytes {
			return nil, nil, "", fmt.Errorf("over %d bytes unpacked", maxWheelBytes)
		}
		sum := sha256.Sum256(data)
		size, err := strconv.ParseInt(r[2], 10, 64)
		if err != nil || size < 0 || size != int64(len(data)) || digest != "sha256="+base64.RawURLEncoding.EncodeToString(sum[:]) {
			return nil, nil, "", fmt.Errorf("%s does not match its RECORD", path)
		}
		out[path] = digest
		// only the dist-info's own files are read again (METADATA, WHEEL): nothing else kept
		if strings.Count(path, "/") == 1 && strings.Contains(path, ".dist-info/") {
			contents[path] = data
		}
	}
	for name := range files {
		if _, ok := out[name]; !ok && name != record.Name {
			return nil, nil, "", fmt.Errorf("%s is not in RECORD", name)
		}
	}
	return out, contents, record.Name, nil
}

// A wheel's limits beyond each member's (MaxMemberBytes): its members
// and their total, declared and read. Variables so a test sets them small.
var (
	maxWheelMembers        = 20000
	maxWheelBytes   uint64 = 1 << 30
)

// compiled is a file Python writes for itself (bytecode). Neither wheel may carry one under
// a plugin's package: Python can run bytecode that is not its source, so the source alone
// is what both wheels hold and what is compared (the release's build removes __pycache__).
func compiled(p string) bool {
	return strings.Contains(p, "/__pycache__/") || strings.HasSuffix(p, ".pyc") || strings.HasSuffix(p, ".pyo")
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
			if compiled(p) {
				return fmt.Errorf("%s is bytecode: a package holds its plugin's source only", p)
			}
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
		if strings.HasPrefix(p, dist+"/") {
			if compiled(p) {
				return fmt.Errorf("the release's pipeline wheel has bytecode %s: it holds the plugin's source only", p)
			}
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

// headerName: an RFC 822 field name, as Python's email parser takes one (printable ASCII,
// no space or colon).
func headerName(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x21 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

// HasHeader: a "Key: value" line of b has that value.
func HasHeader(b []byte, key, value string) bool {
	// the metadata's headers only (RFC 822, as Python's metadata reads them): they end at
	// the first blank line (the description's body follows); a name matches in any case; a
	// value folded onto the next line, or the header given twice, names nothing
	//
	// Python splits lines on CR as well as LF, so a CR could hide a header from this reader:
	// a wheel's headers have none (uv and setuptools write LF), and a header block with a CR
	// names nothing. Python's block then ends no later than this one, on the same lines (the
	// description's body after it may hold any byte). A header line Python would not read as
	// one (a name outside RFC 822's printable characters, or no colon) names nothing too, so
	// every header Python reads is one read here.
	head := b
	if i := bytes.Index(b, []byte("\n\n")); i >= 0 {
		head = b[:i]
	}
	if bytes.IndexByte(head, '\r') >= 0 {
		return false
	}
	seen := 0
	last := ""
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" {
			break
		}
		if line[0] == ' ' || line[0] == '\t' {
			if strings.EqualFold(last, key) {
				return false
			}
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || !headerName(k) {
			return false
		}
		last = k
		if strings.EqualFold(k, key) {
			// Python strips the leading blanks of a value only, so a trailing one is part of it
			if strings.TrimLeft(v, " \t") != value {
				return false
			}
			seen++
		}
	}
	return seen == 1
}
