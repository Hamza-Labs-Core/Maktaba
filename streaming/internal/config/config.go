// Package config loads streaming.toml-shaped configuration without
// pulling a TOML library — for v1 we read environment variables and
// document a TOML example. The shape mirrors the Story 8.1 §4 schema.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config is the resolved runtime config.
type Config struct {
	Server    ServerConfig
	Admin     AdminConfig
	JWT       JWTConfig
	Cache     CacheConfig
	FFmpeg    FFmpegConfig
	HWAccel   HWAccelConfig
	Transcode TranscodeConfig
	Database  DatabaseConfig
}

type ServerConfig struct {
	Addr             string
	ReadHeaderMS     int
	WriteMS          int
	ShutdownMS       int
}

type AdminConfig struct {
	Addr        string
	MetricsAddr string
}

type JWTConfig struct {
	JWKSURL             string
	JWKSRefreshSec      int
	ClockSkewLeewaySec  int
}

type CacheConfig struct {
	Root    string
	MaxGiB  int
	GCEverySec int
}

type FFmpegConfig struct {
	Binary      string
	ProbeBinary string
}

type HWAccelConfig struct {
	Prefer string
}

type TranscodeConfig struct {
	MaxConcurrent int
	QueueDepth    int
}

type DatabaseConfig struct {
	URL string
}

// Load reads from environment with sensible defaults. Each key maps
// onto a streaming.toml section but lives in the env so installers
// can mount one .env across the deployment.
func Load() Config {
	return Config{
		Server: ServerConfig{
			Addr:         envStr("MAKTABA_HTTP_ADDR", ":8081"),
			ReadHeaderMS: envInt("MAKTABA_READ_HEADER_MS", 5000),
			WriteMS:      envInt("MAKTABA_WRITE_MS", 0),
			ShutdownMS:   envInt("MAKTABA_SHUTDOWN_MS", 30000),
		},
		Admin: AdminConfig{
			Addr:        envStr("MAKTABA_ADMIN_ADDR", ":9101"),
			MetricsAddr: envStr("MAKTABA_METRICS_ADDR", ":9091"),
		},
		JWT: JWTConfig{
			JWKSURL:            envStr("MAKTABA_JWKS_URL", ""),
			JWKSRefreshSec:     envInt("MAKTABA_JWKS_REFRESH_SEC", 300),
			ClockSkewLeewaySec: envInt("MAKTABA_CLOCK_SKEW_LEEWAY_SEC", 60),
		},
		Cache: CacheConfig{
			Root:       envStr("MAKTABA_CACHE_ROOT", "/var/maktaba/cache/streaming"),
			MaxGiB:     envInt("MAKTABA_CACHE_MAX_GIB", 50),
			GCEverySec: envInt("MAKTABA_CACHE_GC_SEC", 300),
		},
		FFmpeg: FFmpegConfig{
			Binary:      envStr("MAKTABA_FFMPEG_BIN", "ffmpeg"),
			ProbeBinary: envStr("MAKTABA_FFPROBE_BIN", "ffprobe"),
		},
		HWAccel: HWAccelConfig{
			Prefer: envStr("MAKTABA_HWACCEL_PREFER", "auto"),
		},
		Transcode: TranscodeConfig{
			MaxConcurrent: envInt("MAKTABA_TRANSCODE_MAX_CONCURRENT", 0),
			QueueDepth:    envInt("MAKTABA_TRANSCODE_QUEUE_DEPTH", 4),
		},
		Database: DatabaseConfig{
			URL: envStr("MAKTABA_DATABASE_URL", ""),
		},
	}
}

// LeewayDuration returns the JWT leeway as a duration.
func (c Config) LeewayDuration() time.Duration {
	return time.Duration(c.JWT.ClockSkewLeewaySec) * time.Second
}

// JWKSRefreshDuration returns the refresh interval as a duration.
func (c Config) JWKSRefreshDuration() time.Duration {
	return time.Duration(c.JWT.JWKSRefreshSec) * time.Second
}

// CacheMaxBytes returns the byte cap.
func (c Config) CacheMaxBytes() int64 { return int64(c.Cache.MaxGiB) * 1024 * 1024 * 1024 }

// GCInterval returns the GC sweeper period.
func (c Config) GCInterval() time.Duration { return time.Duration(c.Cache.GCEverySec) * time.Second }

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
