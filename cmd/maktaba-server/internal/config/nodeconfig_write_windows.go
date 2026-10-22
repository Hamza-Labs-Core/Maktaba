//go:build windows

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
	for range 8 {
		tmp := nodeConfigLeftoverName(path, ".tmp")
		f, err := CreateUserOnly(tmp)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		return f, tmp, nil
	}
	return nil, "", fmt.Errorf("no free temp file name beside %s", path)
}

// CreateUserOnly creates the file at path for writing, which must not be there yet (an
// error matching fs.ErrExist when it is), the user's alone from the moment it exists:
// CreateFileW with a protected DACL that grants this process's user, by its SID, full
// control and no one else, and no sharing. Its folder's ACL (Authenticated Users on a
// second drive) is never the file's. A secret the server keeps (the node config, a
// switch's records and identity) is written to one and renamed into place.
func CreateUserOnly(path string) (*os.File, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return nil, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(name, windows.GENERIC_WRITE, 0, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "create", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// MakeUserOnly makes the file at path the user's alone when it is not
// (secureNodeConfig): a secret an older version wrote with its folder's ACL keeps none of
// it.
func MakeUserOnly(path string) error { return secureNodeConfig(path) }

// IsUserOnly: the file at path is this user's alone (a protected DACL that grants this
// process's user full control and no one anything, nodeConfigIsUsers).
func IsUserOnly(path string) (bool, error) { return nodeConfigIsUsers(path) }

// replaceNodeConfig puts body in place of the node config at path (the lock held): a new
// file takes the name with its user-only DACL; one already there is made the user's
// alone first when it is not (secureNodeConfig: #178's writer left node.json with its
// folder's ACL) and replaced with ReplaceFileW, which keeps that ACL
// (replaceKeepingACL). The temp file goes unless it is the only copy left (a node config
// that was there and is gone): then it stays for the pipeline's next load.
func replaceNodeConfig(path string, body []byte) error {
	existed := isFile(path)
	if existed {
		if err := secureNodeConfig(path); err != nil {
			return fmt.Errorf("%s is not the user's alone, and cannot be made so: %w", path, err)
		}
	}
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

// userOnlyDACL matches a DACL (as SDDL) that is protected and grants one SID full
// control and no one anything else: what newNodeConfigTemp gives a node config and the
// pipeline's _set_user_only_dacl sets.
var userOnlyDACL = regexp.MustCompile(`^D:([A-Z]*)\(A;;(FA|GA|0x1f01ff);;;([^)]+)\)$`)

// nodeConfigIsUsers: the node config at path is this user's alone (userOnlyDACL with
// this process's user). The user is compared as SDDL writes it, which is an alias for a
// well-known account ("LA" for the built-in Administrator, a CI runner's user), not
// always its S-1-5-... string.
func nodeConfigIsUsers(path string) (bool, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false, err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false, err
	}
	want, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return false, err
	}
	m, w := userOnlyDACL.FindStringSubmatch(sd.String()), userOnlyDACL.FindStringSubmatch(want.String())
	return m != nil && w != nil && strings.Contains(m[1], "P") && strings.EqualFold(m[3], w[3]), nil
}

// secureNodeConfig makes the node config at path the user's alone when it is not (a
// protected DACL that grants this process's user full control and no one else, as the
// pipeline's _set_user_only_dacl): a node.json #178's writer left with its folder's ACL
// (Authenticated Users may change it, on a second drive) keeps none of that.
func secureNodeConfig(path string) error {
	if ok, err := nodeConfigIsUsers(path); err == nil && ok {
		return nil
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
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
		// the old file under its backup name: a reader may have it open for a moment, and
		// one left whole would be read as the node config were node.json ever missing
		_ = removeWhenFree(backup)
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
		_ = removeWhenFree(backup)
		return true, nil
	}
	if isFile(path) {
		_ = removeWhenFree(tmp)
		_ = removeWhenFree(backup)
	}
	return false, nil
}

// isBusy: err is a sharing violation (another process holds the file without sharing it).
func isBusy(err error) bool { return errors.Is(err, windows.ERROR_SHARING_VIOLATION) }
