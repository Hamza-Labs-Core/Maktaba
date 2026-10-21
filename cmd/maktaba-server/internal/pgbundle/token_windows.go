//go:build windows

package pgbundle

import (
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var procCreateRestrictedToken = windows.NewLazySystemDLL("advapi32.dll").NewProc("CreateRestrictedToken")

// disableMaxPrivilege is CreateRestrictedToken's DISABLE_MAX_PRIVILEGE: every
// privilege but SeChangeNotifyPrivilege removed.
const disableMaxPrivilege = 0x1

// postgresToken is this process's token as postgres.exe may run with it:
// zero when it is no administrator's (postgres runs as is), else a restricted
// copy, made once and kept for every start.
var postgresToken = sync.OnceValues(func() (windows.Token, error) {
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return 0, err
	}
	power, err := windows.CreateWellKnownSid(windows.WinBuiltinPowerUsersSid)
	if err != nil {
		return 0, err
	}
	// what postgres.exe checks (pgwin32_is_admin): an enabled membership of either
	isAdmin, err := windows.Token(0).IsMember(admins)
	if err != nil {
		return 0, err
	}
	isPower, err := windows.Token(0).IsMember(power)
	if err != nil {
		return 0, err
	}
	if !isAdmin && !isPower {
		return 0, nil
	}
	return restrictedToken([]*windows.SID{admins, power})
})

// restrictedToken is this process's token with the groups deny-only and no
// privileges but traversal, as pg_ctl's CreateRestrictedProcess makes it. A
// restricted copy of the caller's own primary token needs no extra privilege
// to start a process with.
func restrictedToken(deny []*windows.SID) (windows.Token, error) {
	var cur windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT, &cur); err != nil {
		return 0, fmt.Errorf("open this process's token: %w", err)
	}
	defer func() { _ = cur.Close() }()
	disable := make([]windows.SIDAndAttributes, len(deny))
	for i, s := range deny {
		disable[i] = windows.SIDAndAttributes{Sid: s}
	}
	var restricted windows.Token
	r, _, e := procCreateRestrictedToken.Call(uintptr(cur), disableMaxPrivilege,
		uintptr(len(disable)), uintptr(unsafe.Pointer(&disable[0])), 0, 0, 0, 0, uintptr(unsafe.Pointer(&restricted)))
	if r == 0 {
		return 0, fmt.Errorf("CreateRestrictedToken: %w", e)
	}
	if err := grantUser(restricted); err != nil {
		_ = restricted.Close()
		return 0, fmt.Errorf("give the restricted token's user its objects: %w", err)
	}
	return restricted, nil
}

// tokenDefaultDacl is TOKEN_DEFAULT_DACL.
type tokenDefaultDacl struct{ DefaultDacl *windows.ACL }

// grantUser adds the token's user, with every right, to its default DACL, as
// pg_ctl's AddUserToTokenDacl does: what the restricted postgres creates (its
// shared memory, events, its own children) would otherwise be open only to the
// Administrators it no longer acts as.
func grantUser(tok windows.Token) error {
	user, err := tok.GetTokenUser()
	if err != nil {
		return err
	}
	var n uint32
	_ = windows.GetTokenInformation(tok, windows.TokenDefaultDacl, nil, 0, &n)
	if n == 0 {
		return errors.New("the token has no default DACL")
	}
	buf := make([]byte, n)
	if err := windows.GetTokenInformation(tok, windows.TokenDefaultDacl, &buf[0], n, &n); err != nil {
		return err
	}
	cur := (*tokenDefaultDacl)(unsafe.Pointer(&buf[0])).DefaultDacl
	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(user.User.Sid),
		},
	}}, cur)
	if err != nil {
		return err
	}
	d := tokenDefaultDacl{DefaultDacl: acl}
	return windows.SetTokenInformation(tok, windows.TokenDefaultDacl, (*byte)(unsafe.Pointer(&d)), uint32(unsafe.Sizeof(d)))
}

// restrict gives cmd the postgres token when this process is an
// administrator's (postgresToken).
func restrict(cmd *exec.Cmd) error {
	tok, err := postgresToken()
	if err != nil || tok == 0 {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	return nil
}
