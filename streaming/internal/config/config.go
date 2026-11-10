// Package config loads streaming.toml-shaped configuration without
// pulling a TOML library — for v1 we read environment variables and
// document a TOML example. The shape mirrors the Story 8.1 §4 schema.
package config

import (
	"os"
	"strconv"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
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
	Addr         string
	ReadHeaderMS int
	WriteMS      int
	ShutdownMS   int
}

type AdminConfig struct {
	Addr        string
	MetricsAddr string
}

type JWTConfig struct {
	JWKSURL string
	// StreamKeyFile is the machine's stream signing key (MAKTABA_STREAM_SIGNING_KEY):
	// when set the signed URLs are verified against it instead of a JWKS.
	StreamKeyFile      string
	JWKSRefreshSec     int
	ClockSkewLeewaySec int
}

type CacheConfig struct {
	Root       string
	MaxGiB     int
	GCEverySec int
}

type FFmpegConfig struct {
	Binary      string
	ProbeBinary string
}

type HWAccelConfig struct {
	Prefer string
	// PreferVAAPI tries VA-API before QuickSync (a NAS, a container: the
	// supervisor sets MAKTABA_HWACCEL_VAAPI_FIRST); RenderDevice is the admin's
	// render node ([streaming] render_device; "": the first that works).
	PreferVAAPI  bool
	RenderDevice string
	// StatusFile is where the video verdict is written for Settings → This
	// machine ("": nowhere).
	StatusFile string
}

type TranscodeConfig struct {
	MaxConcurrent int
	QueueDepth    int
	// The hardware tier's limits (Epic 59 stories 59.04, 59.05), when
	// MaxConcurrent sets none: SoftwareMax transcodes at once in software, of
	// SoftwareMaxHeight lines at most, HardwareMax on a working GPU encoder (0:
	// none, the cores' share).
	SoftwareMax       int
	SoftwareMaxHeight int
	HardwareMax       int
	// MinFreeGiB is the free space on the cache's disk below which no
	// transcode starts (a transcode keeps every segment until its session
	// closes) and the sweep removes the closed sessions' folders it keeps: that
	// many GiB; 0 (default): the smaller of 20 GiB and a tenth of the disk, at
	// least 3 GiB; below 0: none ([media] transcode_min_free_gib in server.toml).
	MinFreeGiB int
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
			Addr:         envStr("MAKTABA_HTTP_ADDR", ports.StreamingPublic.Every()),
			ReadHeaderMS: envInt("MAKTABA_READ_HEADER_MS", 5000),
			WriteMS:      envInt("MAKTABA_WRITE_MS", 0),
			ShutdownMS:   envInt("MAKTABA_SHUTDOWN_MS", 30000),
		},
		Admin: AdminConfig{
			Addr:        envStr("MAKTABA_ADMIN_ADDR", ports.StreamingAdmin.Every()),
			MetricsAddr: envStr("MAKTABA_METRICS_ADDR", ports.StreamingMetrics.Every()),
		},
		JWT: JWTConfig{
			JWKSURL:            envStr("MAKTABA_JWKS_URL", ""),
			StreamKeyFile:      envStr("MAKTABA_STREAM_SIGNING_KEY", ""),
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
			Prefer:       envStr("MAKTABA_HWACCEL_PREFER", "auto"),
			PreferVAAPI:  envStr("MAKTABA_HWACCEL_VAAPI_FIRST", "") == "1",
			RenderDevice: envStr("MAKTABA_RENDER_DEVICE", ""),
			StatusFile:   envStr("MAKTABA_VIDEO_STATUS_FILE", ""),
		},
		Transcode: TranscodeConfig{
			MaxConcurrent:     envInt("MAKTABA_TRANSCODE_MAX_CONCURRENT", 0),
			QueueDepth:        envInt("MAKTABA_TRANSCODE_QUEUE_DEPTH", 4),
			SoftwareMax:       envInt("MAKTABA_TRANSCODE_SOFTWARE_MAX", 0),
			SoftwareMaxHeight: envInt("MAKTABA_TRANSCODE_SOFTWARE_MAX_HEIGHT", 0),
			HardwareMax:       envInt("MAKTABA_TRANSCODE_HARDWARE_MAX", 0),
			MinFreeGiB:        envInt("MAKTABA_TRANSCODE_MIN_FREE_GIB", 0),
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
