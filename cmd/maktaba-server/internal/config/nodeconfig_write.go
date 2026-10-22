package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// maktaba-server writes node.json too (a switch of the home, a standby taking over, the
// home's database CA): the same way as its other writer, the pipeline
// (cluster/config.py _write), so the two never undo each other and a node's private key
// is only ever its user's. One write at a time (lockNodeConfig: the lock the pipeline
// takes), through a temp file that is the user's alone from the moment it exists, put in
// place with ReplaceFileW (on Windows) over a node.json that is the user's alone
// (secureNodeConfig makes one that is not so first), with the pipeline's names for what
// a write that could not finish leaves (every reader takes those as the node config
// while node.json is missing or broken, and the pipeline's next load puts them back).

// nodeConfigLockWait is how long a write waits for another process's write of the same
// node config (the pipeline's _LOCK_WAIT_SEC).
var nodeConfigLockWait = 10 * time.Second

// busyTries and busyWait: a file another process has open (a reader, Python, Go or
// PowerShell) cannot be replaced, removed or opened for a moment, and a write leaves
// node.json missing for a moment; a reader or writer tries again this often (the
// pipeline's _BUSY_TRIES, _BUSY_SLEEP).
const (
	busyTries = 20
	busyWait  = 5 * time.Millisecond
)

// readWait is how long a reader waits out a write under way (writeUnderWay): a write
// holds the lock for all of it, which takes milliseconds, and more on a busy machine (the
// pipeline's _READ_WAIT_SEC). A write still under way after it is an error for the
// reader, never "missing" (tests shorten it).
var readWait = 2 * time.Second

// nodeConfigLeftoverName is a name beside path for one write's backup of the old file
// (".bak") or temp file with the new one (".tmp"), as the pipeline names them
// (_leftover_name): the config's name, the time the write started, the process and 8
// random hex.
func nodeConfigLeftoverName(path, ext string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return filepath.Join(filepath.Dir(path),
		fmt.Sprintf(".%s-%d-%d-%s%s", filepath.Base(path), time.Now().Unix(), os.Getpid(), hex.EncodeToString(b[:]), ext))
}

// nodeConfigBrokenName is where a broken node config is kept when it is moved aside, as
// the pipeline names it (_broken_name: never a leftover's name, removed after a while).
func nodeConfigBrokenName(path string) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s.broken-%d-%s", path, time.Now().Unix(), hex.EncodeToString(b[:]))
}

// nodeConfigLockPath is the write lock beside the node config at path (.<name>.lock).
func nodeConfigLockPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
}

// nodeConfigLeftovers are the leftovers beside the node config at path
// (isNodeConfigLeftover), whole or not.
func nodeConfigLeftovers(path string) []string {
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && isNodeConfigLeftover(filepath.Base(path), e.Name()) {
			out = append(out, filepath.Join(filepath.Dir(path), e.Name()))
		}
	}
	return out
}

// writeUnderWay: reading the node config at path failed with err while a write of it may
// be under way: the file is held by another process (ReplaceFileW, a rename), or it is
// missing, or denied for a moment (a backup being removed), while its lock is held or a
// write's leftovers are beside it (ReplaceFileW renames node.json aside before the new
// one takes its name). A reader waits that out rather than take "missing" for "not
// joined".
func writeUnderWay(path string, err error) bool {
	switch {
	case err == nil:
		return false
	case isBusy(err):
		return true
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrPermission):
		if lockedLately(nodeConfigLockPath(path)) {
			return true
		}
		for _, p := range nodeConfigLeftovers(path) {
			if leftoverOfAWriteUnderWay(path, filepath.Base(p)) {
				return true
			}
		}
	}
	return false
}

// lockedLately: the lock file at lock is there and was made in the last minute (a write
// removes it when it is done; one a process that stopped mid-write left, long ago, is
// no write under way; the pipeline's _locked_lately).
func lockedLately(lock string) bool {
	fi, err := os.Stat(lock)
	if err != nil {
		return false
	}
	age := time.Since(fi.ModTime())
	return age > -time.Minute && age < time.Minute
}

// leftoverOfAWriteUnderWay: name is a leftover a write started in the last minute
// named (the time in its name, nodeConfigLeftoverName; the pipeline's _in_progress): a
// stale one, from a write that could not finish long ago, is no write under way.
func leftoverOfAWriteUnderWay(path, name string) bool {
	prefix := "." + filepath.Base(path) + "-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	stamp, _, ok := strings.Cut(strings.TrimPrefix(name, prefix), "-")
	if !ok {
		return false
	}
	started, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return false
	}
	age := time.Since(time.Unix(started, 0))
	return age > -time.Minute && age < time.Minute
}

// readNodeConfigSteady reads the node config at path from the file that holds it
// (nodeConfigFile), waiting out a write under way (writeUnderWay) for up to readWait: a
// reader never takes a node.json that is being replaced for a missing one. What it read
// from, the bytes, and the error of the last read; a write still under way at the end is
// an error that is not fs.ErrNotExist (errStillWritten), so it is never "not joined".
func readNodeConfigSteady(path string, readFile func(string) ([]byte, error)) (string, []byte, error) {
	deadline := time.Now().Add(readWait)
	for {
		held := nodeConfigFile(path, readFile)
		b, err := readFile(held)
		// a leftover it picked that is gone already, or going (denied while it is removed):
		// the write ended, look again
		vanished := held != path && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission))
		if !vanished && !writeUnderWay(path, err) {
			return held, b, err
		}
		if !time.Now().Before(deadline) {
			return held, nil, errStillWritten(held, err)
		}
		time.Sleep(busyWait)
	}
}

// ErrBeingWritten is in the error of a read of the node config that waited readWait for
// a write still under way (errStillWritten): one serve waits out before it starts.
var ErrBeingWritten = errors.New("a write of it still under way")

// errStillWritten is the error of a read that waited readWait for a write under way:
// what the last read said, but never fs.ErrNotExist (a reader would take that for "not
// joined", and a node for the home).
func errStillWritten(path string, err error) error {
	if errors.Is(err, fs.ErrNotExist) || isBusy(err) {
		return fmt.Errorf("%s is being written (%w after %s): %v", path, ErrBeingWritten, readWait, err)
	}
	return err
}

// heldNodeConfig is the file that holds the node config at path (nodeConfigFile) when
// there is one, waiting out a write under way (a leftover it picked can be gone a
// moment later) for up to readWait; "" when there is none. A write still under way at
// the end keeps path (its reader then says it is being written): a resolver never takes
// the local place for it.
func heldNodeConfig(path string) string {
	deadline := time.Now().Add(readWait)
	for {
		held := nodeConfigFile(path, os.ReadFile)
		if isFile(held) {
			return held
		}
		underWay := writeUnderWay(path, fs.ErrNotExist)
		if held == path && !underWay {
			return ""
		}
		if !time.Now().Before(deadline) {
			if underWay {
				return path
			}
			return ""
		}
		time.Sleep(busyWait)
	}
}

// readNodeConfigLocked reads the node config at path from the file that holds it
// (nodeConfigFile) with the write lock held: no write is under way (that lock is this
// one's own), so only a reader holding the file open is waited out, for a moment.
func readNodeConfigLocked(path string) (string, []byte, error) {
	held := nodeConfigFile(path, os.ReadFile)
	for left := busyTries - 1; ; left-- {
		b, err := os.ReadFile(held) //nolint:gosec // this machine's node config
		if err == nil || left == 0 || !isBusy(err) {
			return held, b, err
		}
		time.Sleep(busyWait)
	}
}

// removeWhenFree removes path, trying again for a moment while another process has it
// open (a reader); gone already is no error.
func removeWhenFree(path string) error {
	for left := busyTries - 1; ; left-- {
		err := os.Remove(path)
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if left == 0 || !isBusy(err) {
			return err
		}
		time.Sleep(busyWait)
	}
}

// WriteNodeConfig changes the node config at path: with the write lock held
// (lockNodeConfig), the fields of the file that holds it (nodeConfigFile; any encoding a
// reader reads, withoutBOM) are read, edit changes them, and they are written through a
// temp file the user's alone from the moment it exists, flushed, then put in place
// (replaceNodeConfig). A node config that is there but cannot be read is never written
// over with less (the error is returned); a broken one is replaced by the edit's fields.
func WriteNodeConfig(path string, edit func(fields map[string]json.RawMessage) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	unlock, err := lockNodeConfig(path, nodeConfigLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	fields := map[string]json.RawMessage{}
	held, b, err := readNodeConfigLocked(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("node config %s: %w", held, err)
	default:
		if json.Unmarshal(withoutBOM(b), &fields) != nil {
			fields = map[string]json.RawMessage{} // broken: nothing of it to keep
		}
	}
	if err := edit(fields); err != nil {
		return err
	}
	out, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return err
	}
	return replaceNodeConfig(path, append(out, '\n'))
}

// UpdateNodeConfig changes some fields of the node config of node nodeID at path: with
// the write lock held it reads the file as it is now (never a copy read earlier, which a
// re-join may have replaced meanwhile), edit changes the fields its call is about, and
// only the fields that changed are written; every other one stays as the file has it. A
// node config that is another node's now (a re-join) is left as it is (an error).
func UpdateNodeConfig(path, nodeID string, edit func(*NodeConfig)) error {
	return WriteNodeConfig(path, func(fields map[string]json.RawMessage) error {
		raw, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		var cur NodeConfig
		if err := json.Unmarshal(raw, &cur); err != nil {
			return fmt.Errorf("node config %s: %w", path, err)
		}
		if got := strings.TrimSpace(cur.NodeID); got != strings.TrimSpace(nodeID) {
			if got == "" {
				return fmt.Errorf("no node config at %s now (node %q's is gone or broken): nothing written", path, nodeID)
			}
			return fmt.Errorf("node config %s is node %q now, not %q (joined again meanwhile): left as it is", path, got, nodeID)
		}
		before, err := nodeConfigFields(cur)
		if err != nil {
			return err
		}
		next := cur
		edit(&next)
		after, err := nodeConfigFields(next)
		if err != nil {
			return err
		}
		for k, v := range after {
			if !bytes.Equal(before[k], v) {
				fields[k] = v
			}
		}
		for k := range before {
			if _, kept := after[k]; !kept {
				delete(fields, k) // an omitted field: none now
			}
		}
		return nil
	})
}

// nodeConfigFields is n as node.json fields.
func nodeConfigFields(n NodeConfig) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(n)
	if err != nil {
		return nil, err
	}
	var out map[string]json.RawMessage
	return out, json.Unmarshal(b, &out)
}

// SetNodeConfigAside moves the node config at path (the file that holds it,
// nodeConfigFile) to aside with the write lock held, so that nothing is read as this
// node config after it (the pipeline's set_aside, a re-join, does the same). The order
// keeps that true at every step a crash can stop it at:
//   - every other leftover of it goes first (tried again while a reader has it open); one
//     that is whole and stays is an error, and nothing has moved yet;
//   - a broken node.json, when the config is in a leftover, is kept aside as
//     node.json.broken-<time>-<hex> (never read as a node config), the user's alone;
//   - then the one file that holds the config takes the name aside, and is made the
//     user's alone.
//
// A node config already moved there is no error.
func SetNodeConfigAside(path, aside string) error {
	unlock, err := lockNodeConfig(path, nodeConfigLockWait)
	if err != nil {
		return err
	}
	defer unlock()
	held := nodeConfigFile(path, os.ReadFile)
	for _, p := range nodeConfigLeftovers(path) {
		if p == held {
			continue
		}
		if err := removeWhenFree(p); err != nil {
			if b, rerr := os.ReadFile(p); rerr != nil || wholeNodeConfig(b) { //nolint:gosec // beside the node config
				return fmt.Errorf("%s, which every reader would take as the node config after it is set aside, "+
					"cannot be removed: %w", p, err)
			}
		}
	}
	if held != path && isFile(path) {
		broken := nodeConfigBrokenName(path)
		if err := renameNoReplace(path, broken); err != nil {
			return fmt.Errorf("the broken %s: %w", path, err)
		}
		// it may hold part of a credential, and it stays for a while: the user's alone,
		// whatever ACL it had (best effort: it is never read as a node config)
		_ = MakeUserOnly(broken)
	}
	if err := renameWhenFree(held, aside); err != nil && (!errors.Is(err, fs.ErrNotExist) || !isFile(aside)) {
		return err
	}
	// the identity it holds (a private key) keeps the ACL of the file it was: the user's
	// alone, whatever that was (a leftover an older writer left with its folder's ACL;
	// best effort, and again when it is read, ReadNodeConfigAt)
	_ = MakeUserOnly(aside)
	return nil
}

// renameWhenFree renames from to to (replacing it), trying again for a moment while
// another process has one of them open (a reader).
func renameWhenFree(from, to string) error {
	for left := busyTries - 1; ; left-- {
		err := os.Rename(from, to)
		if err == nil || left == 0 || !isBusy(err) {
			return err
		}
		time.Sleep(busyWait)
	}
}

// writeSynced writes b to f, flushes it to disk and closes it.
func writeSynced(f *os.File, b []byte) error {
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
