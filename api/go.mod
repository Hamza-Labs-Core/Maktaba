module github.com/Hamza-Labs-Core/Maktaba/api

go 1.23

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/health/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/pressly/goose/v3 v3.22.1
	golang.org/x/crypto v0.27.0
	golang.org/x/term v0.24.0
)

require (
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/health/go => ../shared/health/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go
