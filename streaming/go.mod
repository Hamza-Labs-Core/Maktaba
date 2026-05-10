module github.com/Hamza-Labs-Core/Maktaba/streaming

go 1.23

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/health/go v0.0.0
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/go-chi/chi/v5 v5.1.0
	github.com/google/uuid v1.6.0
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/health/go => ../shared/health/go

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../shared/log/go
