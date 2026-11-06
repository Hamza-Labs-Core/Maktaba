module github.com/Hamza-Labs-Core/Maktaba/cloud

go 1.26.0

// No multipath TCP on our listeners (Go 1.24+ turns it on by default): it was the path
// into CVE-2026-98432, the Linux IPv4 TCP use-after-free (docs/security/advisories/2026-11-01-linux-tcp-uaf.md).
godebug multipathtcp=0

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/go-chi/chi/v5 v5.4.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/lib/pq v1.10.9
	github.com/pressly/goose/v3 v3.29.0
	golang.org/x/crypto v0.58.0
)

require (
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
)

// The shared logger (Epic 53 story 53.01); cloud/Dockerfile copies it beside cloud/.
replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go
