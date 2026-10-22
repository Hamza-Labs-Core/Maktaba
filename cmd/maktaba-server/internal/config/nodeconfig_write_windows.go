//go:build windows

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/layout"
)

// lockNodeConfig holds the write lock of the node config at path: a byte-range lock
// (LockFileEx, byte 0) on .<name>.lock beside it, the one the pipeline takes
// (msvcrt.locking, cluster/config.py _one_at_a_time), waiting up to wait for another
// process's write or recovery. Two writes at once can undo each other inside
// ReplaceFileW (one that fails puts back the file it had opened, over the other's new
// one). release unlocks and removes the lock file unless another process has it open;
// the OS lets the lock go when the process ends.
func lockNodeConfig(path string, wait time.Duration) (release func(), err error) {
	lock := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".lock")
	deadline := time.Now().Add(wait)
	denied := 0
	for {
		f, err := layout.LockFile(lock)
		switch {
		case err == nil:
			return func() {
				layout.Unlock(f)
				_ = os.Remove(lock) // fails while another process has it open: it stays theirs
			}, nil
		case errors.Is(err, layout.ErrLocked):
			if !time.Now().Before(deadline) {
				return nil, fmt.Errorf("another write of the node config holds %s", lock)
			}
		case errors.Is(err, fs.ErrPermission) && denied < busyTries:
			denied++ // access is denied for a moment while another process removes it
		default:
			return nil, fmt.Errorf("the node config's lock %s: %w", lock, err)
		}
		time.Sleep(busyWait)
	}
}

// newNodeConfigTemp creates the temp file of one write beside path
// (nodeConfigLeftoverName), the user's alone from the moment it exists: CreateFileW with a
// protected DACL that grants this process's user, by its SID, full control and no one
// else, CREATE_NEW, and no sharing, so no other handle can be opened while the secret is
// written (the pipeline's _create_user_only). Its folder's ACL (Authenticated Users on a
// second drive) is never the file's.
func newNodeConfigTemp(path string) (*os.File, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, "", err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	for range 8 {
		tmp := nodeConfigLeftoverName(path, ".tmp")
		name, err := windows.UTF16PtrFromString(tmp)
		if err != nil {
			return nil, "", err
		}
		h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if errors.Is(err, windows.ERROR_FILE_EXISTS) {
			continue
		}
		if err != nil {
			return nil, "", &os.PathError{Op: "create", Path: tmp, Err: err}
		}
		return os.NewFile(uintptr(h), tmp), tmp, nil
	}
	return nil, "", fmt.Errorf("no free temp file name beside %s", path)
}

// replaceNodeConfig puts body in place of the node config at path (the lock held): a new
// file takes the name with its user-only DACL; one already there is replaced with
// ReplaceFileW, which keeps that file's ACL (replaceKeepingACL). The temp file goes
// unless it is the only copy left (a node config that was there and is gone): then it
// stays for the pipeline's next load.
func replaceNodeConfig(path string, body []byte) error {
	existed := isFile(path)
	f, tmp, err := newNodeConfigTemp(path)
	if err != nil {
		return err
	}
	if err := writeSynced(f, body); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if !existed {
		err = os.Rename(tmp, path)
	} else {
		err = replaceKeepingACL(path, tmp)
	}
	if err != nil && (isFile(path) || !existed) {
		_ = os.Remove(tmp)
	}
	return err
}

// replaceFile is ReplaceFileW: tmp takes the place (and the ACL) of path, whose old file
// is kept under backup. Tests replace it.
var replaceFile = func(path, tmp, backup string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(tmp)
	if err != nil {
		return err
	}
	b, err := windows.UTF16PtrFromString(backup)
	if err != nil {
		return err
	}
	r, _, callErr := procReplaceFileW.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(t)),
		uintptr(unsafe.Pointer(b)), 0, 0, 0)
	if r == 0 {
		return &os.LinkError{Op: "ReplaceFileW", Old: tmp, New: path, Err: callErr}
	}
	return nil
}

var procReplaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replaceKeepingACL replaces path with tmp through ReplaceFileW and a backup name the
// pipeline recovers from (the pipeline's _replace_keeping_acl): a sharing violation
// (another process has node.json open; nothing moved) is tried again for a moment, and a
// failure half way is settled (settleNodeConfig) before it is returned. The only copy of
// a node's identity is never removed.
func replaceKeepingACL(path, tmp string) error {
	backup := nodeConfigLeftoverName(path, ".bak")
	var err error
	for left := busyTries - 1; ; left-- {
		err = replaceFile(path, tmp, backup)
		if err == nil || !errors.Is(err, windows.ERROR_SHARING_VIOLATION) || left == 0 ||
			!isFile(path) || !isFile(tmp) || isFile(backup) {
			break
		}
		time.Sleep(busyWait)
	}
	if err == nil {
		_ = os.Remove(backup)
		return nil
	}
	settled, serr := settleNodeConfig(path, tmp, backup)
	switch {
	case serr != nil:
		return serr
	case settled:
		return nil
	}
	return err
}

// settleNodeConfig, after a ReplaceFileW that failed (the pipeline's _settle): path holds
// the old file again (put back from backup without replacing: another write that found
// node.json gone and put its new one there keeps it) or, when the old file is gone, the
// new one (tmp, the user's alone from its creation). What is left over goes only once
// path holds a file. True when the new file took the name.
func settleNodeConfig(path, tmp, backup string) (bool, error) {
	if !isFile(path) && isFile(backup) {
		_ = renameNoReplace(backup, path)
	}
	if !isFile(path) && isFile(tmp) {
		if err := os.Rename(tmp, path); err != nil {
			return false, err
		}
		_ = os.Remove(backup)
		return true, nil
	}
	if isFile(path) {
		_ = os.Remove(tmp)
		_ = os.Remove(backup)
	}
	return false, nil
}

// isBusy: err is a sharing violation (another process holds the file without sharing it).
func isBusy(err error) bool { return errors.Is(err, windows.ERROR_SHARING_VIOLATION) }
