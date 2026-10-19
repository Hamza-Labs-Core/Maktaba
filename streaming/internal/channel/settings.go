package channel

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The Maktaba TV settings the streaming service follows. They are rules
// sections the web settings page edits (app_settings rows rules.tv and
// rules.tv_output, holding only what was changed); whatever is not
// changed there keeps the streaming host's MAKTABA_CHANNEL_* value, else
// the built-in default.

// OutputSettings is how a channel on air is encoded (rules tv_output),
// read when a station starts.
type OutputSettings struct {
	// Encoder preference: auto | nvenc | videotoolbox | software.
	Encoder        string
	Width, Height  int
	FPS            int
	VideoKbps      int
	AudioCodec     string // aac | ac3
	AudioKbps      int
	SegmentSeconds int
	// KeyintSeconds is the wall-clock IDR grid (story 33.4): 1-10, default 2.
	KeyintSeconds int
	// MaxActive is how many channels this host keeps on air at once; 0 =
	// auto (EffectiveMaxActive: 16 with VideoToolbox, else 8).
	MaxActive int
	// WarmGrace keeps a channel on air after its last viewer.
	WarmGrace time.Duration
	// NVENCMax is the process-wide NVENC session cap; VTMax the
	// VideoToolbox one (0 = no cap).
	NVENCMax int
	VTMax    int
}

// DefaultPerHostCapVideoToolbox is the auto channels-on-air cap when the
// channels encode with VideoToolbox (Apple Silicon has several media
// engines and many cores; an M3 Ultra carries far more than 8).
const DefaultPerHostCapVideoToolbox = 16

// EffectiveMaxActive is the channels-on-air cap for the resolved encoder:
// the setting, else 16 with VideoToolbox, else DefaultPerHostCap.
func (o OutputSettings) EffectiveMaxActive(encoder string) int {
	if o.MaxActive > 0 {
		return o.MaxActive
	}
	if encoder == EncoderVideoToolbox {
		return DefaultPerHostCapVideoToolbox
	}
	return DefaultPerHostCap
}

// TVSettings are the rules tv values the streaming service reads.
type TVSettings struct {
	// HorizonDays is how far ahead schedules are planned; the XMLTV
	// guide shows as far.
	HorizonDays int
}

// Defaults (the rules' defaults; env overrides them, settings override env).
const (
	DefaultHorizonDays = 7
	maxHorizonDays     = 31
)

// OutputSettingsFromEnv is today's MAKTABA_CHANNEL_* configuration.
func OutputSettingsFromEnv() OutputSettings {
	p, enc := ProfileFromEnv()
	return OutputSettings{
		Encoder: enc, Width: p.Width, Height: p.Height, FPS: p.FPS, VideoKbps: p.VideoKbps,
		AudioCodec: p.AudioCodec, AudioKbps: p.AudioKbps, SegmentSeconds: p.SegmentSeconds,
		KeyintSeconds: clampInt(envInt("MAKTABA_CHANNEL_KEYINT_SEC", DefaultKeyintSeconds), 1, 10),
		MaxActive: envInt("MAKTABA_CHANNEL_MAX_ACTIVE", 0),
		WarmGrace: time.Duration(envInt("MAKTABA_CHANNEL_WARM_GRACE_SEC", int(DefaultWarmGrace/time.Second))) * time.Second,
		NVENCMax:  envInt("MAKTABA_CHANNEL_NVENC_MAX", DefaultNVENCMaxSessions),
		VTMax:     envInt("MAKTABA_CHANNEL_VT_MAX", 0),
	}
}

// TVSettingsFromEnv is the env fallback of the tv rules.
func TVSettingsFromEnv() TVSettings {
	return TVSettings{
		HorizonDays: clampInt(envInt("MAKTABA_CHANNEL_HORIZON_DAYS", DefaultHorizonDays), 1, maxHorizonDays),
	}
}

// Profile is the output profile for these settings with the resolved
// encoder.
func (o OutputSettings) Profile(enc EncoderChoice) OutputProfile {
	p := DefaultProfile()
	p.Width, p.Height, p.FPS, p.VideoKbps = o.Width, o.Height, o.FPS, o.VideoKbps
	p.AudioCodec, p.AudioKbps, p.SegmentSeconds = o.AudioCodec, o.AudioKbps, o.SegmentSeconds
	p.KeyintSeconds = o.KeyintSeconds
	p.Encoder, p.VTSoftware, p.VTPrioSpeed = enc.Name, enc.VTSoftware, enc.VTPrioSpeed
	return p.normalised()
}

// ApplyOutputRules overlays the stored rules.tv_output changes on base. A
// value that cannot be used is skipped (named in bad) and base's applies.
func ApplyOutputRules(base OutputSettings, raw []byte) (out OutputSettings, bad []string) {
	out = base
	m := decodeRules(raw)
	for name, v := range m {
		ok := true
		switch name {
		case "encoder":
			s, _ := jsonString(v)
			switch s = strings.ToLower(s); s {
			case "auto", EncoderNVENC, EncoderVideoToolbox, EncoderSoftware:
				out.Encoder = s
			default:
				ok = false
			}
		case "resolution":
			s, _ := jsonString(v)
			if w, h, good := parseResolution(s); good {
				out.Width, out.Height = w, h
			} else {
				ok = false
			}
		case "fps":
			ok = setInt(&out.FPS, v, 10, 60)
		case "video_kbps":
			ok = setInt(&out.VideoKbps, v, 300, 50000)
		case "audio_codec":
			s, _ := jsonString(v)
			if s = strings.ToLower(s); s == "aac" || s == "ac3" {
				out.AudioCodec = s
			} else {
				ok = false
			}
		case "audio_kbps":
			ok = setInt(&out.AudioKbps, v, 64, 640)
		case "segment_seconds":
			ok = setInt(&out.SegmentSeconds, v, 1, 10)
		case "keyint_seconds":
			ok = setInt(&out.KeyintSeconds, v, 1, 10)
		case "max_active_channels":
			ok = setInt(&out.MaxActive, v, 0, 64)
		case "warm_grace_seconds":
			var sec int
			if ok = setInt(&sec, v, 0, 3600); ok {
				out.WarmGrace = time.Duration(sec) * time.Second
			}
		case "nvenc_max_sessions":
			ok = setInt(&out.NVENCMax, v, 1, 64)
		case "videotoolbox_max_sessions":
			ok = setInt(&out.VTMax, v, 0, 64)
		default:
			continue // a setting this service does not use
		}
		if !ok {
			bad = append(bad, name)
		}
	}
	return out, bad
}

// ApplyTVRules overlays the stored rules.tv changes this service reads.
func ApplyTVRules(base TVSettings, raw []byte) (out TVSettings, bad []string) {
	out = base
	for name, v := range decodeRules(raw) {
		switch name {
		case "horizon_days":
			if !setInt(&out.HorizonDays, v, 1, maxHorizonDays) {
				bad = append(bad, name)
			}
		}
	}
	return out, bad
}

func decodeRules(raw []byte) map[string]json.RawMessage {
	var m map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func jsonString(v json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(v, &s) != nil {
		return "", false
	}
	return s, true
}

func setInt(dst *int, v json.RawMessage, lo, hi int) bool {
	var f float64
	if json.Unmarshal(v, &f) != nil || f != float64(int(f)) || int(f) < lo || int(f) > hi {
		return false
	}
	*dst = int(f)
	return true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func parseResolution(s string) (int, int, bool) {
	w, h, ok := strings.Cut(strings.ToLower(strings.TrimSpace(s)), "x")
	if !ok {
		return 0, 0, false
	}
	wi, err1 := strconv.Atoi(strings.TrimSpace(w))
	hi, err2 := strconv.Atoi(strings.TrimSpace(h))
	if err1 != nil || err2 != nil || wi < 160 || hi < 90 || wi > 7680 || hi > 4320 {
		return 0, 0, false
	}
	return wi, hi, true
}

// Settings is what the streaming service reads its TV settings from.
type Settings interface {
	TV(ctx context.Context) TVSettings
	Output(ctx context.Context) OutputSettings
}

// StripSource is implemented by settings that also carry the strip's
// defaults (rules tv_strip).
type StripSource interface {
	Strip(ctx context.Context) StripSettings
}

// StaticSettings serves fixed settings (tests, no database).
type StaticSettings struct {
	TVs    TVSettings
	Outs   OutputSettings
	Strips *StripSettings // nil: the built-in strip defaults
}

func (s StaticSettings) TV(context.Context) TVSettings         { return s.TVs }
func (s StaticSettings) Output(context.Context) OutputSettings { return s.Outs }
func (s StaticSettings) Strip(context.Context) StripSettings {
	if s.Strips != nil {
		return *s.Strips
	}
	return DefaultStripSettings()
}

// settingsTTL: how long a read of the settings is reused.
const settingsTTL = 10 * time.Second

// PGSettings reads the rules from app_settings (cached for a few
// seconds), over the env fallback. A failed read keeps the last good
// values (or the fallback).
type PGSettings struct {
	Pool    *pgxpool.Pool
	BaseTV  TVSettings
	BaseOut OutputSettings
	Logger  *slog.Logger
	TTL     time.Duration

	mu      sync.Mutex
	at      time.Time
	tv      TVSettings
	out     OutputSettings
	strip   StripSettings
	loaded  bool
	lastBad string
}

// NewPGSettings builds the store with the env fallbacks.
func NewPGSettings(pool *pgxpool.Pool, logger *slog.Logger) *PGSettings {
	return &PGSettings{Pool: pool, BaseTV: TVSettingsFromEnv(), BaseOut: OutputSettingsFromEnv(), Logger: logger}
}

func (s *PGSettings) TV(ctx context.Context) TVSettings {
	s.refresh(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tv
}

func (s *PGSettings) Output(ctx context.Context) OutputSettings {
	s.refresh(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out
}

// Strip is rules.tv_strip over the built-in strip defaults.
func (s *PGSettings) Strip(ctx context.Context) StripSettings {
	s.refresh(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.strip
}

func (s *PGSettings) refresh(ctx context.Context) {
	ttl := s.TTL
	if ttl <= 0 {
		ttl = settingsTTL
	}
	s.mu.Lock()
	if s.loaded && time.Since(s.at) < ttl {
		s.mu.Unlock()
		return
	}
	if !s.loaded {
		s.tv, s.out, s.strip = s.BaseTV, s.BaseOut, DefaultStripSettings()
	}
	s.mu.Unlock()

	raws := map[string][]byte{}
	if s.Pool != nil {
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		rows, err := s.Pool.Query(qctx,
			`SELECT key, value::text FROM app_settings WHERE key IN ('rules.tv', 'rules.tv_output', 'rules.tv_strip')`)
		if err == nil {
			for rows.Next() {
				var k, v string
				if rows.Scan(&k, &v) == nil {
					raws[k] = []byte(v)
				}
			}
			err = rows.Err()
			rows.Close()
		}
		cancel()
		if err != nil {
			s.mu.Lock()
			s.at, s.loaded = time.Now(), true // keep the last good values; retry after the TTL
			s.mu.Unlock()
			return
		}
	}
	tv, badTV := ApplyTVRules(s.BaseTV, raws["rules.tv"])
	out, badOut := ApplyOutputRules(s.BaseOut, raws["rules.tv_output"])
	strip, badStrip := ApplyStripRules(DefaultStripSettings(), raws["rules.tv_strip"], false)
	s.mu.Lock()
	s.tv, s.out, s.strip, s.at, s.loaded = tv, out, strip, time.Now(), true
	bad := strings.Join(append(append(badTV, badOut...), badStrip...), ",")
	logBad := bad != "" && bad != s.lastBad
	s.lastBad = bad
	s.mu.Unlock()
	if logBad && s.Logger != nil {
		s.Logger.Warn("channels: settings skipped (defaults apply)", "settings", bad, "event", "channel_settings_invalid")
	}
}
