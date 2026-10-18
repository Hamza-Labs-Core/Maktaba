package channel

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// MediaInfo is what the block builder needs to know about a source.
type MediaInfo struct {
	HasVideo bool
	HasAudio bool
}

// Prober inspects a source (file path or URL).
type Prober interface {
	Probe(ctx context.Context, source string) (MediaInfo, error)
}

// FFprobe is the production Prober with a small TTL cache (a channel
// replays the same filler and bumpers all day).
type FFprobe struct {
	Bin string
	TTL time.Duration

	mu    sync.Mutex
	cache map[string]probeEntry
}

type probeEntry struct {
	info MediaInfo
	at   time.Time
}

// NewFFprobe builds a cached ffprobe Prober.
func NewFFprobe(bin string) *FFprobe {
	if bin == "" {
		bin = "ffprobe"
	}
	return &FFprobe{Bin: bin, TTL: 30 * time.Minute, cache: map[string]probeEntry{}}
}

// Probe lists the source's stream types.
func (f *FFprobe) Probe(ctx context.Context, source string) (MediaInfo, error) {
	f.mu.Lock()
	if e, ok := f.cache[source]; ok && time.Since(e.at) < f.TTL {
		f.mu.Unlock()
		return e.info, nil
	}
	f.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, f.Bin, "-v", "error", "-show_entries", "stream=codec_type",
		"-of", "csv=p=0", source)
	out, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, err
	}
	info := parseStreamTypes(string(out))
	if !info.HasVideo && !info.HasAudio {
		return MediaInfo{}, errors.New("probe: no audio or video streams")
	}
	f.mu.Lock()
	f.cache[source] = probeEntry{info: info, at: time.Now()}
	f.mu.Unlock()
	return info, nil
}

func parseStreamTypes(out string) MediaInfo {
	var info MediaInfo
	for _, l := range strings.Split(out, "\n") {
		switch strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(l), ",")) {
		case "video":
			info.HasVideo = true
		case "audio":
			info.HasAudio = true
		}
	}
	return info
}
