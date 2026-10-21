package config

import (
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The copied keys and passwords are this user's only (protected DACL, one ACE for
// the user), as the node config's icacls /inheritance:r /grant:r is; server.toml
// keeps its folder's access.
func TestCopiedKeysAreTheUsersOnly(t *testing.T) {
	t.Parallel()
	old, cur, env := winLayout(t)
	plant(t, old.Config, map[string]string{"server.toml": "[server]\n", "secrets.key": "S", "postgres-maktaba.password": "P"})
	migrateWin(t, osMover, old, cur, env)
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	only := func(path string) bool {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		ctrl, _, err := sd.Control()
		if err != nil {
			t.Fatal(err)
		}
		dacl, _, err := sd.DACL()
		if err != nil {
			t.Fatal(err)
		}
		if ctrl&windows.SE_DACL_PROTECTED == 0 || dacl == nil || dacl.AceCount != 1 {
			return false
		}
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		return ace.Header.AceType == windows.ACCESS_ALLOWED_ACE_TYPE && sid.Equals(user.User.Sid)
	}
	for _, name := range []string{"secrets.key", "postgres-maktaba.password"} {
		if !only(filepath.Join(cur.Keys, name)) {
			t.Errorf("%s: its ACL must name this user only, inheritance off", name)
		}
	}
	if only(filepath.Join(cur.Config, "server.toml")) {
		t.Error("server.toml is not a key: it keeps its folder's access")
	}
}

// AppData on a server (a redirected folder) roams.
func TestProfileRoamsOnAServer(t *testing.T) {
	t.Parallel()
	if !profileRoams(`\\server\profiles\me\AppData\Roaming`) {
		t.Fatal("AppData on a server roams")
	}
}
