//go:build windows

package nodecopy

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
)

// restrictACL leaves only this account and the system on a path's ACL (icacls: no
// inherited entries; a directory's passed on to what is in it).
func restrictACL(path string) error {
	u, err := user.Current()
	if err != nil {
		return err
	}
	grant := ":F"
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		grant = ":(OI)(CI)F"
	}
	out, err := exec.Command("icacls", path, "/inheritance:r", "/grant:r", u.Username+grant, "/grant:r", "*S-1-5-18"+grant).CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
