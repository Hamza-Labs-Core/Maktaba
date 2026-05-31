module github.com/Hamza-Labs-Core/Maktaba/streaming

go 1.25.0

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/health/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/go-chi/chi/v5 v5.3.0
	github.com/google/uuid v1.6.0
	google.golang.org/grpc v1.81.1
)

require (
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/health/go => ../shared/health/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go
