module github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-launcher

go 1.25.0

// No multipath TCP on our listeners (Go 1.24+ turns it on by default): it was the path
// into CVE-2026-98432, the Linux IPv4 TCP use-after-free (docs/security/advisories/2026-11-01-linux-tcp-uaf.md).
godebug multipathtcp=0

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/go/layout v0.0.0
	golang.org/x/sys v0.45.0
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/go/layout => ../../shared/go/layout
