//go:build windows

package nodecopy

import (
	"os"
	"os/exec"
	"os/user"
)

// restrictACL leaves only this account and the system on a path's ACL (icacls: no
// inherited entries); best effort, the copy works either way.
func restrictACL(path string) {
	u, err := user.Current()
	if err != nil {
		return
	}
	grant := ":F"
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		grant = ":(OI)(CI)F"
	}
	_ = exec.Command("icacls", path, "/inheritance:r", "/grant:r", u.Username+grant, "/grant:r", "*S-1-5-18"+grant).Run()
}
