module github.com/Hamza-Labs-Core/Maktaba/streaming

go 1.23

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/health/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.68.1
)

require (
	golang.org/x/net v0.29.0 // indirect
	golang.org/x/sys v0.25.0 // indirect
	golang.org/x/text v0.18.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240903143218-8af14fe29dc1 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/health/go => ../shared/health/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go
