module github.com/Hamza-Labs-Core/Maktaba/shared/logkeeper/go

go 1.25.8

require (
	github.com/Hamza-Labs-Core/Maktaba/shared/log/go v0.0.0
	github.com/klauspost/compress v1.20.1
	golang.org/x/sys v0.47.0
	modernc.org/sqlite v1.51.0
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.72.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)

replace github.com/Hamza-Labs-Core/Maktaba/shared/log/go => ../../log/go
