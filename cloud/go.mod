module github.com/Hamza-Labs-Core/Maktaba/cloud

go 1.26.0

// No multipath TCP on our listeners (Go 1.24+ turns it on by default): it was the path
// into CVE-2026-98432, the Linux IPv4 TCP use-after-free (docs/security/advisories/2026-11-01-linux-tcp-uaf.md).
godebug multipathtcp=0

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go v0.0.0
	github.com/fxamacker/cbor/v2 v2.9.4
	github.com/go-chi/chi/v5 v5.4.0
	github.com/go-webauthn/webauthn v0.18.2
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/lib/pq v1.10.9
	github.com/pressly/goose/v3 v3.29.0
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
)

require (
	github.com/dustin/go-humanize v1.1.0 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/go-webauthn/x v0.3.1 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/google/go-tpm v0.9.8 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/ncruces/go-strftime v1.1.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/sethvargo/go-retry v0.5.0 // indirect
	github.com/tinylib/msgp v1.6.4 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	modernc.org/libc v1.78.0 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.60.1 // indirect
)

// The shared logger (Epic 53 story 53.01); cloud/Dockerfile copies it beside cloud/.
replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go => ../shared/logkeeper/go
