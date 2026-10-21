package config

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// profileRoams reports a profile whose AppData (appdata) leaves this PC: on a
// server (\\server\share, a redirected folder) or a network drive, or a roaming
// user profile (CentralProfile set in its ProfileList entry).
func profileRoams(appdata string) bool {
	if strings.HasPrefix(appdata, `\\`) {
		return true
	}
	if vol := filepath.VolumeName(appdata); vol != "" {
		if root, err := windows.UTF16PtrFromString(vol + `\`); err == nil && windows.GetDriveType(root) == windows.DRIVE_REMOTE {
			return true
		}
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion\ProfileList\`+user.User.Sid.String(), registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer func() { _ = k.Close() }()
	central, _, err := k.GetStringValue("CentralProfile")
	return err == nil && strings.TrimSpace(central) != ""
}

// renameNoReplace renames from to to unless to exists (MoveFileEx without
// MOVEFILE_REPLACE_EXISTING): a copy never replaces a file another process put
// there meanwhile. The error then matches fs.ErrExist.
func renameNoReplace(from, to string) error {
	f, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	t, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(f, t, 0); err != nil {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: err}
	}
	return nil
}

// restrictToUser limits path to the user this process runs as, inheritance off:
// the same as the node config's icacls /inheritance:r /grant:r <user>:F (the
// pipeline's cluster/config.py). A key file's copy would otherwise get its folder's
// ACL (SYSTEM and Administrators too).
func restrictToUser(path string) error {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, nil)
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
}
