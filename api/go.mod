module github.com/Hamza-Labs-Core/Maktaba/api

go 1.23

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/health/go v0.0.0-00010101000000-000000000000
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/tracing/go v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/go-playground/validator/v10 v10.22.1
	github.com/google/uuid v1.6.0
	github.com/lib/pq v1.10.9
	github.com/pressly/goose/v3 v3.22.1
	golang.org/x/crypto v0.28.0
	golang.org/x/sys v0.26.0
	golang.org/x/term v0.25.0
	golang.org/x/text v0.19.0
	golang.org/x/time v0.7.0
	google.golang.org/grpc v1.68.1
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.6 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_golang v1.20.5 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/net v0.30.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/health/go => ../shared/health/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/metrics/go => ../shared/metrics/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/tracing/go => ../shared/tracing/go
