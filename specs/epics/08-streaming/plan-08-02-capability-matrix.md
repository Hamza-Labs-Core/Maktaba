# Implementation Plan — Story 8.2 Capability Matrix & Profile Registry

> Companion to [story-08-02-capability-matrix.md](story-08-02-capability-matrix.md).
> The story states *what* and *why*; this plan states *how*. Builds on
> the route surface from [Story 8.1](plan-08-01-server-skeleton.md) and
> consumes probe rows from [Story 8.15](plan-08-15-probe-cache.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `streaming/internal/caps` — pure Go, no I/O after boot. |
| Storage of the matrix | Embedded YAML (`go:embed`), one document per profile. Operators can override per profile via `streaming.toml [caps] override_dir = "/etc/maktaba/streaming/caps.d/"`. |
| Hot-reload | Reload the YAML and per-session overrides on `LISTEN profiles_changed` (Postgres) — same channel/cadence as the API's settings reload. The schema lives in [Epic 7 Story 7.0](../07-api-server/story-07-00-foundations.md), so we just SUBSCRIBE here. |
| Decision API | Pure function `Decide(profile, media MediaInfo, override SessionOverride) Verdict`. No globals. |
| Out of scope | The `force_software` and `burn_subs` overrides flow through here as Verdict modifiers but the FFmpeg-level handling is Story 8.5/8.7. The matrix only decides *which lane*. |

## 1. Architecture diagram

```
            ┌─────────────────────────────────────────────────────┐
            │  embed/profiles/*.yaml   (browser-chrome.yaml,      │
            │                            browser-safari.yaml,     │
            │                            ios-native.yaml,         │
            │                            android-native.yaml,     │
            │                            tvos.yaml, androidtv.yaml│
            │                            generic.yaml)            │
            └────────────────────┬────────────────────────────────┘
                                 │ go:embed → init()
                                 ▼
            ┌─────────────────────────────────────────────────────┐
            │  caps.Registry  (sync.Map[string]Profile)           │
            │   - Profile struct loaded from YAML                  │
            │   - operator overrides from --caps-dir              │
            │   - LISTEN profiles_changed → Reload()              │
            └────────────────────┬────────────────────────────────┘
                                 │
   media (probe.MediaInfo)       │       SessionOverride
   ──────────────────────────────┼─────────────────────────────────
                                 ▼
            ┌─────────────────────────────────────────────────────┐
            │  caps.Decide(profile, media, override) → Verdict    │
            │                                                     │
            │  Verdict.Mode       = direct | remux | transcode    │
            │  Verdict.Container  = "mp4" | "mkv" | "ts" | …      │
            │  Verdict.Reason     = human-readable, surfaced in   │
            │                       structured logs               │
            │  Verdict.Ladder     = []Rendition (for transcode)   │
            │  Verdict.AudioMode  = "passthrough" | "transcode"   │
            │  Verdict.SubMode    = "external" | "burn-in"        │
            └────────────────────┬────────────────────────────────┘
                                 │
                                 ▼
                  Story 8.3 (direct), 8.4 (remux), 8.5/8.6/8.7 (transcode)
```

The Verdict is the single intermediate value all three lanes consume; if
a story needs more bits later (e.g., Story 8.5's bitrate ladder), they
hang off `Verdict` so callers don't recompute the same matrix decision.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `streaming/internal/caps/profile.go` | `Profile`, `CodecRule`, `ContainerRule`, YAML loader. |
| `streaming/internal/caps/registry.go` | `Registry` (thread-safe map), `Reload`, `LISTEN profiles_changed` driver. |
| `streaming/internal/caps/decide.go` | `Decide(...)`, `Verdict`, `MediaInfo`, `SessionOverride`. |
| `streaming/internal/caps/ladder.go` | `BuildLadder(media, override) []Rendition` — used when transcode is the verdict. |
| `streaming/internal/caps/profiles/*.yaml` | Embedded profile definitions (one file per profile). |
| `streaming/internal/caps/profile_test.go` | YAML round-trip + schema-failure tests. |
| `streaming/internal/caps/decide_test.go` | Table-driven tests for the matrix. |
| `streaming/internal/caps/registry_test.go` | Reload, LISTEN-driven hot-reload, override directory. |
| `streaming/configs/profiles/README.md` | Operator-facing doc on YAML schema and override layering. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `streaming/cmd/maktaba-streaming/main.go` | Wire `caps.Registry` into the dependency container; subscribe to `profiles_changed`. |
| `streaming/internal/server/server.go` | Inject `caps.Registry` into handlers via `Dependencies`. |
| `streaming/configs/streaming.toml.example` | Add `[caps] override_dir`. |
| `specs/epics/08-streaming/README.md` | Tick 8.2 once landed. |

### 2.3 Type definitions

```go
// streaming/internal/caps/profile.go
package caps

import (
    "fmt"
    "io/fs"
    "regexp"

    "gopkg.in/yaml.v3"
)

// Profile is what one of the 7 client classes can decode. The YAML mirrors
// this struct one-for-one.
type Profile struct {
    Name        string         `yaml:"name"`
    Containers  []string       `yaml:"containers"`   // 'mp4', 'mkv', 'webm', 'ts'
    Video       []CodecRule    `yaml:"video"`
    Audio       []CodecRule    `yaml:"audio"`
    HDR         []string       `yaml:"hdr"`          // 'sdr', 'hdr10', 'dolby-vision'
    MaxResolution Resolution   `yaml:"max_resolution"`
    MaxBitrateKbps int         `yaml:"max_bitrate_kbps"`
    Notes       string         `yaml:"notes,omitempty"`
}

type Resolution struct {
    Width  int `yaml:"width"`
    Height int `yaml:"height"`
}

// CodecRule expresses "this codec is supported under these constraints."
//   - Codec is the canonical name from ffprobe (`h264`, `hevc`, `av1`, `aac`, `ac3`).
//   - Profiles is the set of FOURCC profiles or container profile strings
//     accepted; absent means "all profiles for this codec".
//   - MaxLevel is an optional H.264/HEVC level cap (e.g., "5.1").
//   - MaxChannels caps audio channel count (1, 2, 6, 8). 0 = unlimited.
type CodecRule struct {
    Codec       string   `yaml:"codec"`
    Profiles    []string `yaml:"profiles,omitempty"`
    MaxLevel    string   `yaml:"max_level,omitempty"`
    MaxChannels int      `yaml:"max_channels,omitempty"`
}

func (p *Profile) Validate() error {
    if p.Name == "" {
        return fmt.Errorf("profile: missing name")
    }
    if len(p.Containers) == 0 {
        return fmt.Errorf("profile %q: must list at least one container", p.Name)
    }
    if len(p.Video) == 0 {
        return fmt.Errorf("profile %q: must list at least one video codec", p.Name)
    }
    return nil
}
```

```go
// streaming/internal/caps/decide.go
package caps

// MediaInfo is the subset of probe.ProbeRow that decides the mode. Story
// 8.15 supplies the full struct; we extract only what we need so this
// package doesn't depend on probe.
type MediaInfo struct {
    Container        string  // 'mp4', 'mkv', 'webm', 'ts'
    DurationSec      float64
    BitrateKbps      int     // overall stream bitrate

    VideoCodec       string  // 'h264', 'hevc', 'av1', 'vp9'
    VideoProfile     string  // e.g. 'High', 'Main10'
    VideoLevel       string  // e.g. '5.1'
    Width, Height    int
    HDR              string  // 'sdr', 'hdr10', 'dolby-vision'
    HasBFrames       bool

    AudioCodec       string  // 'aac', 'ac3', 'eac3', 'dts', 'opus'
    AudioChannels    int
    AudioBitrateKbps int
}

// SessionOverride applies per-OpenSession knobs (Story 8.8).
type SessionOverride struct {
    ForceTranscode bool
    ForceSoftware  bool   // resolved by Story 8.7, mirrored here so Verdict logs reflect it
    BurnSubs       bool
    MaxBitrateKbps int    // 0 = no cap
    MaxHeight      int    // 0 = no cap (used when slot pressure picks 720p — Story 8.10)
}

type Mode string

const (
    ModeDirect    Mode = "direct"
    ModeRemux     Mode = "remux"
    ModeTranscode Mode = "transcode"
)

type Verdict struct {
    Mode       Mode
    Container  string      // target container for remux; source container for direct
    Reason     string      // why we picked this lane (single human-readable sentence)
    Ladder     []Rendition // populated when Mode == transcode
    AudioMode  string      // "passthrough" | "transcode"
    SubMode    string      // "external" | "burn-in"
}

type Rendition struct {
    Name        string  // "1080p", "720p", "480p"
    Width       int
    Height      int
    BitrateKbps int
    Codec       string  // "h264"
    Profile     string  // "high", "main", "baseline"
    Level       string  // "4.0"
}

// Decide is a pure function. Implementation order:
//   1. Apply SessionOverride.ForceTranscode → ModeTranscode (skip the rest of the matrix).
//   2. Check container is in profile.Containers.
//   3. Check video codec rule (codec, profile, level).
//   4. Check audio codec rule.
//   5. Check HDR.
//   6. Check resolution and bitrate caps.
//   7. If everything passes → ModeDirect.
//   8. If only the container differs → ModeRemux (target picked from
//      profile.Containers preference order).
//   9. Otherwise → ModeTranscode (caller invokes BuildLadder).
//
// Each branch sets Verdict.Reason so logs show the deciding factor.
func Decide(p *Profile, m MediaInfo, o SessionOverride) Verdict { ... }
```

### 2.4 Function signatures

```go
// streaming/internal/caps/registry.go
package caps

func New(operatorOverrideDir string) (*Registry, error)

func (r *Registry) Get(profileName string) *Profile  // returns "generic" if unknown
func (r *Registry) Reload(ctx context.Context) error
func (r *Registry) Subscribe(ctx context.Context, listener pgxlisten.Listener) error
```

## 3. The matrix — embedded YAML

`streaming/internal/caps/profiles/browser-chrome.yaml`:

```yaml
name: browser-chrome
containers:
  - mp4
  - webm
  - ts
video:
  - codec: h264
    profiles: [baseline, main, high, high10]
    max_level: "5.2"
  - codec: vp9
  - codec: av1
audio:
  - codec: aac
    max_channels: 6
  - codec: opus
hdr:
  - sdr
max_resolution:
  width:  3840
  height: 2160
max_bitrate_kbps: 25000
notes: "HEVC requires user enabling chrome://flags; assume false."
```

`streaming/internal/caps/profiles/browser-safari.yaml`:

```yaml
name: browser-safari
containers:
  - mp4
  - mov
  - ts
  - mkv          # Safari 14+ via fragmented MP4 box reading
video:
  - codec: h264
    profiles: [baseline, main, high, high10]
    max_level: "5.2"
  - codec: hevc            # Safari plays HEVC on macOS 11+ / iOS 11+
    profiles: [main, main10]
audio:
  - codec: aac
    max_channels: 6
  - codec: ac3             # passthrough on AppleTV via Safari → AVPlayer
hdr:
  - sdr
  - hdr10
  - dolby-vision
max_resolution:
  width:  3840
  height: 2160
max_bitrate_kbps: 30000
notes: "HEVC direct-play is the headline difference vs browser-chrome."
```

`streaming/internal/caps/profiles/ios-native.yaml`:

```yaml
name: ios-native
containers: [mp4, mov, ts]
video:
  - codec: h264
    profiles: [baseline, main, high]
    max_level: "5.2"
  - codec: hevc
    profiles: [main, main10]
audio:
  - codec: aac
    max_channels: 6
hdr: [sdr, hdr10, dolby-vision]
max_resolution: { width: 3840, height: 2160 }
max_bitrate_kbps: 30000
notes: "AC3 is NOT direct-playable on iOS — must remux audio to AAC."
```

`streaming/internal/caps/profiles/android-native.yaml`:

```yaml
name: android-native
containers: [mp4, ts, mkv, webm]
video:
  - codec: h264
    profiles: [baseline, main, high]
    max_level: "5.1"
  - codec: hevc
    profiles: [main, main10]
  - codec: av1
  - codec: vp9
audio:
  - codec: aac
  - codec: opus
  - codec: ac3
hdr: [sdr, hdr10]
max_resolution: { width: 3840, height: 2160 }
max_bitrate_kbps: 25000
```

`streaming/internal/caps/profiles/tvos.yaml`:

```yaml
name: tvos
containers: [mp4, ts]
video:
  - codec: h264
    profiles: [main, high]
    max_level: "5.2"
  - codec: hevc
    profiles: [main, main10]
audio:
  - codec: aac
  - codec: ac3            # AppleTV passthrough to AVR
  - codec: eac3
hdr: [sdr, hdr10, dolby-vision]
max_resolution: { width: 3840, height: 2160 }
max_bitrate_kbps: 40000
```

`streaming/internal/caps/profiles/androidtv.yaml`:

```yaml
name: androidtv
containers: [mp4, ts, mkv]
video:
  - codec: h264
    profiles: [main, high]
  - codec: hevc
    profiles: [main, main10]
  - codec: vp9
  - codec: av1
audio:
  - codec: aac
  - codec: ac3
  - codec: eac3
  - codec: opus
hdr: [sdr, hdr10]
max_resolution: { width: 3840, height: 2160 }
max_bitrate_kbps: 30000
```

`streaming/internal/caps/profiles/generic.yaml`:

```yaml
name: generic
containers: [ts, mp4]
video:
  - codec: h264
    profiles: [baseline, main, high]
    max_level: "4.0"
audio:
  - codec: aac
    max_channels: 2
hdr: [sdr]
max_resolution: { width: 1280, height: 720 }
max_bitrate_kbps: 4500
notes: "HLS H.264+AAC at 720p — what every player tested in the last 10 years can decode."
```

## 4. Ladder construction (`ladder.go`)

```go
// streaming/internal/caps/ladder.go
package caps

// Default ladder rungs. Each rung is one rendition; the build trims based
// on source resolution, bitrate cap, and (Story 8.10) slot-pressure caps.
var defaultRungs = []Rendition{
    {Name: "1080p", Width: 1920, Height: 1080, BitrateKbps: 4500, Codec: "h264", Profile: "high", Level: "4.0"},
    {Name: "720p",  Width: 1280, Height:  720, BitrateKbps: 2500, Codec: "h264", Profile: "main", Level: "3.1"},
    {Name: "480p",  Width:  854, Height:  480, BitrateKbps:  900, Codec: "h264", Profile: "main", Level: "3.0"},
}

// BuildLadder applies, in order:
//   1. Drop rungs above source resolution (no upscaling).
//   2. Drop rungs above MaxBitrateKbps from profile or override.
//   3. Drop rungs above SessionOverride.MaxHeight (Story 8.10's degraded path).
//   4. Always keep ≥ 1 rung; if every rung is dropped, emit a single rung
//      sized to min(source, profile.max_resolution) at a CRF-based default.
func BuildLadder(p *Profile, m MediaInfo, o SessionOverride) []Rendition {
    cap := p.MaxBitrateKbps
    if o.MaxBitrateKbps > 0 && o.MaxBitrateKbps < cap {
        cap = o.MaxBitrateKbps
    }
    maxH := p.MaxResolution.Height
    if o.MaxHeight > 0 && o.MaxHeight < maxH {
        maxH = o.MaxHeight
    }
    if m.Height > 0 && m.Height < maxH {
        maxH = m.Height
    }

    out := make([]Rendition, 0, len(defaultRungs))
    for _, r := range defaultRungs {
        if r.Height > maxH {
            continue
        }
        if cap > 0 && r.BitrateKbps > cap {
            continue
        }
        out = append(out, r)
    }
    if len(out) == 0 {
        out = append(out, fallbackRung(maxH, cap))
    }
    return out
}
```

## 5. Decision logic (`decide.go`)

```go
// streaming/internal/caps/decide.go (continued)

func Decide(p *Profile, m MediaInfo, o SessionOverride) Verdict {
    // 1. Hard override.
    if o.ForceTranscode {
        return Verdict{
            Mode:      ModeTranscode,
            Container: "ts",
            Reason:    "force_transcode override",
            Ladder:    BuildLadder(p, m, o),
            AudioMode: "transcode",
            SubMode:   sub(o),
        }
    }
    if o.BurnSubs {
        // burn-in implies a video filter pass → forced transcode.
        return Verdict{
            Mode:      ModeTranscode,
            Container: "ts",
            Reason:    "burn_subs requires transcode (subtitle filter pass)",
            Ladder:    BuildLadder(p, m, o),
            AudioMode: "transcode",
            SubMode:   "burn-in",
        }
    }

    videoOK, videoReason := matchVideo(p, m)
    audioOK, audioReason := matchAudio(p, m)
    hdrOK := containsString(p.HDR, m.HDR)
    resOK := m.Width <= p.MaxResolution.Width && m.Height <= p.MaxResolution.Height
    brOK := p.MaxBitrateKbps == 0 || m.BitrateKbps <= p.MaxBitrateKbps
    cntOK := containsString(p.Containers, m.Container)

    bitrateCap := o.MaxBitrateKbps > 0 && m.BitrateKbps > o.MaxBitrateKbps

    switch {
    case videoOK && audioOK && hdrOK && resOK && brOK && cntOK && !bitrateCap:
        return Verdict{
            Mode:      ModeDirect,
            Container: m.Container,
            Reason:    "direct-play eligible",
            AudioMode: "passthrough",
            SubMode:   sub(o),
        }
    case videoOK && audioOK && hdrOK && resOK && brOK && !cntOK && !bitrateCap:
        return Verdict{
            Mode:      ModeRemux,
            Container: pickRemuxTarget(p, m),
            Reason:    "container mismatch, codecs OK",
            AudioMode: "passthrough",
            SubMode:   sub(o),
        }
    default:
        reason := strings.Join(filterEmpty(videoReason, audioReason,
            unmatchedHDR(hdrOK, m.HDR), unmatchedRes(resOK, m, p),
            unmatchedBitrate(brOK || bitrateCap, m, p, o)), "; ")
        return Verdict{
            Mode:      ModeTranscode,
            Container: "ts",
            Reason:    reason,
            Ladder:    BuildLadder(p, m, o),
            AudioMode: "transcode",
            SubMode:   sub(o),
        }
    }
}

func sub(o SessionOverride) string {
    if o.BurnSubs {
        return "burn-in"
    }
    return "external"
}

func matchVideo(p *Profile, m MediaInfo) (bool, string) {
    for _, r := range p.Video {
        if r.Codec != m.VideoCodec {
            continue
        }
        if len(r.Profiles) > 0 && !containsString(r.Profiles, strings.ToLower(m.VideoProfile)) {
            return false, fmt.Sprintf("video profile %q unsupported", m.VideoProfile)
        }
        if r.MaxLevel != "" && parseLevel(m.VideoLevel) > parseLevel(r.MaxLevel) {
            return false, fmt.Sprintf("video level %s exceeds %s", m.VideoLevel, r.MaxLevel)
        }
        return true, ""
    }
    return false, fmt.Sprintf("video codec %q not in profile", m.VideoCodec)
}
```

`pickRemuxTarget` picks the first container in `p.Containers` that the
project knows how to write with `-c copy` and that is in the player's
preferred order — for browser-chrome that's `mp4` (CMAF/fMP4), for
tvos/ios it's `ts`.

## 6. Test plan

### 6.1 Profile YAML tests (`profile_test.go`)

| Test | What it pins |
|---|---|
| `TestProfile_AllEmbeddedAreValid` | Loads every embedded YAML, asserts `Validate` passes; pins schema. |
| `TestProfile_OverrideDirReplacesEmbedded` | A `caps.d/browser-chrome.yaml` with `max_bitrate_kbps: 999` shadows the embedded copy after `Reload`. |
| `TestProfile_OverrideMissingNameRejected` | YAML with empty `name` → load error; the bad file does not knock other profiles out of the registry. |
| `TestProfile_UnknownContainerYieldsValidationError` | A YAML containing `containers: [foo]` is rejected at load time (we keep the canonical list closed). |

### 6.2 Decision-table tests (`decide_test.go`)

The matrix is best tested as a giant table. One row per documented case:

| Case | Profile | Source | Override | Expected Mode | Expected Reason fragment |
|---|---|---|---|---|---|
| H.264 high MP4 AAC stereo | browser-chrome | mp4 / h264 high / aac 2ch | — | direct | direct-play eligible |
| H.264 high MKV AAC stereo | browser-chrome | mkv / h264 high / aac 2ch | — | remux | container mismatch |
| HEVC main10 MP4 on Chrome | browser-chrome | mp4 / hevc main10 / aac | — | transcode | video codec "hevc" not in profile |
| HEVC main10 MP4 on Safari | browser-safari | mp4 / hevc main10 / aac | — | direct | direct-play eligible |
| AC3 audio on iOS | ios-native | mp4 / h264 high / ac3 6ch | — | remux *or* transcode | audio codec "ac3" not in profile (forces transcode of audio; remux not enough — the test asserts `AudioMode=transcode`) |
| 4K HDR10 on AppleTV | tvos | mp4 / hevc main10 / ac3 / hdr10 | — | direct | direct-play eligible |
| 4K HDR10 on AndroidTV | androidtv | mkv / hevc main10 / ac3 / hdr10 | — | remux | container mismatch |
| Force-transcode with direct-eligible source | browser-chrome | mp4 / h264 / aac | force_transcode=true | transcode | force_transcode override |
| Bitrate cap below source | browser-chrome | mp4 / h264 / 6000 kbps | max_bitrate_kbps=1500 | transcode | bitrate cap |
| Burn-subs forces transcode | ios-native | mp4 / h264 / aac | burn_subs=true | transcode | burn_subs requires transcode |
| HDR Dolby Vision on Chrome | browser-chrome | mp4 / hevc / dolby-vision | — | transcode | hdr "dolby-vision" not in profile |
| Source resolution > profile cap | generic | mp4 / h264 / 1080p | — | transcode | resolution 1920x1080 exceeds 1280x720 |
| Source bitrate > profile cap | generic | mp4 / h264 / 720p / 8000kbps | — | transcode | bitrate 8000 exceeds 4500 |
| 7.1 audio on a profile capping at 6 channels | browser-chrome | mp4 / h264 / aac 8ch | — | transcode | audio channels exceed cap |
| AV1 in MP4 on Safari (older Safari) | browser-safari | mp4 / av1 / aac | — | transcode | video codec "av1" not in profile |

Implementation:

```go
// decide_test.go
type row struct {
    name     string
    profile  string
    media    caps.MediaInfo
    override caps.SessionOverride
    mode     caps.Mode
    reason   string // substring match, not exact
}

var rows = []row{
    { "h264 mp4 aac on chrome", "browser-chrome",
      caps.MediaInfo{Container: "mp4", VideoCodec: "h264", VideoProfile: "High",
                     VideoLevel: "4.0", Width: 1920, Height: 1080,
                     AudioCodec: "aac", AudioChannels: 2, BitrateKbps: 4000,
                     HDR: "sdr"},
      caps.SessionOverride{}, caps.ModeDirect, "direct-play eligible" },
    // ... see table above
}

func TestDecide(t *testing.T) {
    reg, err := caps.New("")
    require.NoError(t, err)

    for _, r := range rows {
        t.Run(r.name, func(t *testing.T) {
            v := caps.Decide(reg.Get(r.profile), r.media, r.override)
            require.Equal(t, r.mode, v.Mode, "verdict.Reason=%q", v.Reason)
            require.Contains(t, v.Reason, r.reason)
        })
    }
}
```

### 6.3 Registry tests (`registry_test.go`)

| Test | What it pins |
|---|---|
| `TestRegistry_NewLoadsAllEmbedded` | All 7 profiles present after `New("")`. |
| `TestRegistry_GetUnknownReturnsGeneric` | `Get("gibberish")` returns the `generic` profile and logs a warning with the supplied name (AC-3). |
| `TestRegistry_OverrideDir` | An override YAML for `browser-chrome` shadows the embedded copy; other profiles unaffected. |
| `TestRegistry_HotReloadOnNotify` | Spy on the listener; emit a fake `profiles_changed` event; `Get` returns the reloaded profile. |
| `TestRegistry_ReloadAtomicity` | A failing override file does not blow away the previously good in-memory profile. |
| `TestRegistry_ConcurrentGet` | 1000 goroutines call `Get` while a reload is in flight; no torn reads. |

### 6.4 Ladder tests (`ladder_test.go`)

| Test | What it pins |
|---|---|
| `TestLadder_Default` | 1080p source with no override → 3 rungs (1080p, 720p, 480p). |
| `TestLadder_BitrateCap1500` | 1080p source with `MaxBitrateKbps=1500` → only 480p (the only rung ≤ 1500). |
| `TestLadder_NoUpscale` | 480p source with no override → only 480p, even though defaults list 1080p/720p above it. |
| `TestLadder_MaxHeight720` | Override `MaxHeight=720` → 720p + 480p only. |
| `TestLadder_AlwaysOneRung` | Cap so aggressive that no default rung fits → fallback rung at the cap height/bitrate. |

## 7. Test code scaffolding

```go
// streaming/internal/caps/registry_test.go
package caps_test

import (
    "context"
    "os"
    "path/filepath"
    "testing"
    "time"

    "github.com/stretchr/testify/require"

    "maktaba/streaming/internal/caps"
)

func TestRegistry_GetUnknownReturnsGeneric(t *testing.T) {
    reg, err := caps.New("")
    require.NoError(t, err)
    p := reg.Get("totally-made-up")
    require.Equal(t, "generic", p.Name)
    require.Equal(t, 720, p.MaxResolution.Height)
}

func TestRegistry_OverrideDir(t *testing.T) {
    dir := t.TempDir()
    require.NoError(t, os.WriteFile(
        filepath.Join(dir, "browser-chrome.yaml"),
        []byte(`name: browser-chrome
containers: [mp4]
video:
  - codec: h264
    profiles: [main]
audio:
  - codec: aac
hdr: [sdr]
max_resolution: { width: 1920, height: 1080 }
max_bitrate_kbps: 999
`), 0o644))

    reg, err := caps.New(dir)
    require.NoError(t, err)
    require.Equal(t, 999, reg.Get("browser-chrome").MaxBitrateKbps)
    // The other embedded profiles are unaffected.
    require.NotEqual(t, 999, reg.Get("ios-native").MaxBitrateKbps)
}

func TestRegistry_HotReloadOnNotify(t *testing.T) {
    dir := t.TempDir()
    require.NoError(t, os.WriteFile(
        filepath.Join(dir, "browser-chrome.yaml"),
        []byte(`name: browser-chrome
containers: [mp4]
video:
  - codec: h264
audio: [{codec: aac}]
hdr: [sdr]
max_resolution: { width: 1920, height: 1080 }
max_bitrate_kbps: 1
`), 0o644))

    reg, err := caps.New(dir)
    require.NoError(t, err)
    require.Equal(t, 1, reg.Get("browser-chrome").MaxBitrateKbps)

    // Bump the file and trigger reload.
    require.NoError(t, os.WriteFile(
        filepath.Join(dir, "browser-chrome.yaml"),
        []byte(`name: browser-chrome
containers: [mp4]
video:
  - codec: h264
audio: [{codec: aac}]
hdr: [sdr]
max_resolution: { width: 1920, height: 1080 }
max_bitrate_kbps: 9001
`), 0o644))

    require.NoError(t, reg.Reload(context.Background()))
    require.Equal(t, 9001, reg.Get("browser-chrome").MaxBitrateKbps)
}
```

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Profile claims H.265 but client actually fails to decode | Out of scope — the user can flip `force_transcode=true` per session (Story 8.8). The matrix only encodes statically-known capabilities. | Story 8.8 override path |
| Profile registry update without restart | `LISTEN profiles_changed` triggers `Reload`; existing sessions keep their original Verdict (sticky), new ones see the new matrix. | `TestRegistry_HotReloadOnNotify` |
| Operator override file is invalid | The bad file is logged with its filename and ignored; previously good in-memory profile stays. | `TestRegistry_ReloadAtomicity` |
| Unknown client profile name from API | `Get()` falls back to `generic` and emits a warning with the supplied name + request UA (logged by middleware in Story 8.1). | `TestRegistry_GetUnknownReturnsGeneric` |
| Source with no audio (silent video) | `MediaInfo.AudioCodec == ""`; `matchAudio` short-circuits as OK; mode tracks the video decision. | Decide table row `silent-video-direct`. |
| Multiple audio tracks (multi-language) | The matrix is asked per-track at session-open time (the API picks one via `audio_track`); 8.5's transcode pipeline can passthrough one and transcode another. The Verdict here reflects the chosen track only. | Documented in §6.2 row "ac3 + aac dual" — Decide is called twice. |
| HDR source on SDR-only profile (Chrome) | Matrix marks transcode. Story 8.5's tone-map filter (`zscale=t=linear,tonemap=hable,zscale=t=bt709`) handles the actual conversion; Verdict.Reason carries `"hdr 'hdr10' not in profile"`. | `TestDecide_HDRDolbyVisionOnChrome` |
| Profile has empty `max_bitrate_kbps` (0) | Treated as "no cap"; ladder builder skips the cap branch. | `TestLadder_NoCap`. |
| Override `MaxHeight` but source is below | No-op (`maxH = min(source, override)`); Verdict stays direct if otherwise eligible. | Implicit in `TestLadder_NoUpscale`. |
| Two source video tracks (multi-angle MKV) | We pick `video[0]`; the second track is ignored. Multi-angle isn't a Maktaba feature. | Decide reads `MediaInfo.VideoCodec` (singular). |

## 9. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `gopkg.in/yaml.v3` | ^3.0 | Standard YAML for Go; supports the embedded `mapping` style we use. |
| `github.com/jackc/pgx/v5/pgxlisten` | ^5.5 | LISTEN/NOTIFY driver; only used in `Subscribe`. |

(Already pulled in by Story 8.1; no new top-level deps.)

## 10. Acceptance checklist

**Matrix correctness (story ACs)**
- [ ] AC-1: `canDirectPlay(profile, media)` returns true iff every (container, video, audio) tuple is allowed at the client's profile/level.
- [ ] AC-2: `force_transcode=true` and `max_bitrate_kbps=N` overrides are honored before the static matrix.
- [ ] AC-3: Unknown profile name → `generic` profile is used, warning is logged with the supplied name and UA.

**Test coverage**
- [ ] All 15+ table rows in §6.2 pass.
- [ ] HEVC on chrome → transcode; HEVC on safari → direct (the "smoking gun" pair).
- [ ] AC-3 audio on ios-native → AudioMode=transcode (even when video could direct).
- [ ] Ladder tests in §6.4 all pass.

**Hot reload**
- [ ] `LISTEN profiles_changed` triggers an in-place reload.
- [ ] An invalid override file is rejected without dropping the previously good profile.

**Docs**
- [ ] `streaming/configs/profiles/README.md` documents the YAML schema and override layering.
- [ ] `specs/epics/08-streaming/README.md` ticks 8.2.
