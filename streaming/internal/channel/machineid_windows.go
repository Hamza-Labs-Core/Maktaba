package channel

import (
	"strings"

	winreg "golang.org/x/sys/windows/registry"
)

// machineGUID is Windows' id of this PC (HKLM\SOFTWARE\Microsoft\Cryptography
// MachineGuid, the 64-bit view), which a node config is claimed with; "" when it
// cannot be read.
func machineGUID() string {
	k, err := winreg.OpenKey(winreg.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		winreg.QUERY_VALUE|winreg.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}
