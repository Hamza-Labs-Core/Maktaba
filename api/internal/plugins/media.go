package plugins

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// Budgets of one job (54.05 §3).
const (
	MaxRead       = 8 << 20
	MaxFrames     = 2000
	MaxFrameEdge  = 1280
	MaxAudioMs    = 10 * 60 * 1000
	mediaDeadline = 2 * time.Minute
)

// Media hands a plugin the bytes, frames and audio of the job's video (54.05): a handle
// bound to the invocation, the video and the file as it was when opened. Paths never
// leave the API. Frames and audio are made by the home's ffmpeg.
type Media struct {
	// FFmpeg is the ffmpeg binary ("" finds it on PATH).
	FFmpeg string
}

type handle struct {
	video string
	f     *os.File
	size  int64
	mtime time.Time
}

func (h *handle) close() { _ = h.f.Close() }

// file answers a video's path, size and duration.
func (c *call) file(video string) (string, float64, *Refusal) {
	var path string
	var dur sql.NullFloat64
	if err := c.s.DB.QueryRowContext(c.ctx, `SELECT path, duration_sec FROM videos WHERE id = $1`, video).Scan(&path, &dur); err != nil {
		return "", 0, dbErr(err)
	}
	return path, dur.Float64, nil
}

func (m *Media) ffmpeg() string {
	if m != nil && m.FFmpeg != "" {
		return m.FFmpeg
	}
	return "ffmpeg"
}

func runFFmpeg(ctx context.Context, bin string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, mediaDeadline)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin"}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

func (s *Server) mediaRoutes() {
	s.routes["media/open"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string `json:"video"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		path, _, ref := c.file(in.Video)
		if ref != nil {
			return nil, ref
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, refuse(503, CodeMediaGone, "the file is not reachable")
		}
		st, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, refuse(503, CodeMediaGone, "the file is not reachable")
		}
		c.inv.mu.Lock()
		defer c.inv.mu.Unlock()
		if c.inv.ended {
			_ = f.Close()
			return nil, refuse(401, CodeToken, "the job ended")
		}
		c.inv.nextHandle++
		id := c.inv.nextHandle
		c.inv.handles[id] = &handle{video: in.Video, f: f, size: st.Size(), mtime: st.ModTime()}
		return map[string]any{"handle": id}, nil
	}
	get := func(c *call, id uint64) (*handle, *Refusal) {
		c.inv.mu.Lock()
		h := c.inv.handles[id]
		c.inv.mu.Unlock()
		if h == nil {
			return nil, refuse(404, CodeNotFound, "no such handle")
		}
		// the file under a handle must not change: a plugin never mixes two versions
		st, err := os.Stat(h.f.Name())
		if err != nil {
			return nil, refuse(503, CodeMediaGone, "the file is not reachable")
		}
		if st.Size() != h.size || !st.ModTime().Equal(h.mtime) {
			return nil, refuse(409, CodeMediaChanged, "the file changed")
		}
		return h, nil
	}
	s.routes["media/read"] = func(c *call) (any, *Refusal) {
		var in struct {
			Handle uint64 `json:"handle"`
			Offset int64  `json:"offset"`
			Len    int64  `json:"len"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if in.Len < 0 || in.Len > MaxRead || in.Offset < 0 {
			return nil, refuse(413, CodeTooLarge, "at most 8 MB a read")
		}
		h, ref := get(c, in.Handle)
		if ref != nil {
			return nil, ref
		}
		c.inv.mu.Lock()
		over := c.inv.mediaBytes+in.Len > 2*h.size
		if !over {
			c.inv.mediaBytes += in.Len
		}
		c.inv.mu.Unlock()
		if over {
			return nil, refuse(403, CodeQuota, "a job reads at most twice the file")
		}
		buf := make([]byte, in.Len)
		n, err := h.f.ReadAt(buf, in.Offset)
		if err != nil && n == 0 && in.Len > 0 && in.Offset < h.size {
			return nil, refuse(503, CodeMediaGone, "read failed")
		}
		return binary(buf[:n]), nil
	}
	s.routes["media/size"] = func(c *call) (any, *Refusal) {
		var in struct {
			Handle uint64 `json:"handle"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		h, ref := get(c, in.Handle)
		if ref != nil {
			return nil, ref
		}
		return map[string]any{"size": h.size}, nil
	}
	s.routes["media/close"] = func(c *call) (any, *Refusal) {
		var in struct {
			Handle uint64 `json:"handle"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		c.inv.mu.Lock()
		if h := c.inv.handles[in.Handle]; h != nil {
			h.close()
			delete(c.inv.handles, in.Handle)
		}
		c.inv.mu.Unlock()
		return map[string]any{}, nil
	}
	s.routes["media/frame"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video   string `json:"video"`
			TMs     int64  `json:"t_ms"`
			MaxEdge int    `json:"max_edge"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if in.MaxEdge <= 0 || in.MaxEdge > MaxFrameEdge {
			return nil, refuse(413, CodeTooLarge, "max_edge is 1..1280")
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		path, dur, ref := c.file(in.Video)
		if ref != nil {
			return nil, ref
		}
		if in.TMs < 0 || (dur > 0 && float64(in.TMs) >= dur*1000) {
			return nil, refuse(400, CodeInvalid, "t_ms is inside the video")
		}
		c.inv.mu.Lock()
		c.inv.frames++
		over := c.inv.frames > MaxFrames
		c.inv.mu.Unlock()
		if over {
			return nil, refuse(403, CodeQuota, "at most 2,000 frames a job")
		}
		edge := strconv.Itoa(in.MaxEdge)
		out, err := runFFmpeg(c.ctx, c.s.Media.ffmpeg(), "-ss", fmt.Sprintf("%.3f", float64(in.TMs)/1000), "-i", path,
			"-frames:v", "1", "-vf", "scale=w="+edge+":h="+edge+":force_original_aspect_ratio=decrease", "-f", "image2pipe", "-c:v", "png", "pipe:1")
		if err != nil || len(out) == 0 {
			return nil, refuse(503, CodeMediaGone, "no frame")
		}
		return binary(out), nil
	}
	s.routes["media/audio"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string `json:"video"`
			T0Ms  int64  `json:"t0_ms"`
			T1Ms  int64  `json:"t1_ms"`
			Rate  int    `json:"rate"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if in.Rate != 8000 && in.Rate != 16000 && in.Rate != 22050 {
			return nil, refuse(400, CodeInvalid, "rate is 8000, 16000 or 22050")
		}
		span := in.T1Ms - in.T0Ms
		if in.T0Ms < 0 || span <= 0 {
			return nil, refuse(400, CodeInvalid, "t0_ms < t1_ms")
		}
		if span > MaxAudioMs {
			return nil, refuse(413, CodeTooLarge, "at most 10 minutes a call")
		}
		if ref := c.writeScope(in.Video); ref != nil {
			return nil, ref
		}
		path, dur, ref := c.file(in.Video)
		if ref != nil {
			return nil, ref
		}
		c.inv.mu.Lock()
		over := dur > 0 && float64(c.inv.audioMs+span) > 4*dur*1000
		if !over {
			c.inv.audioMs += span
		}
		c.inv.mu.Unlock()
		if over {
			return nil, refuse(403, CodeQuota, "a job hears at most four times the video")
		}
		out, err := runFFmpeg(c.ctx, c.s.Media.ffmpeg(), "-ss", fmt.Sprintf("%.3f", float64(in.T0Ms)/1000), "-t",
			fmt.Sprintf("%.3f", float64(span)/1000), "-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(in.Rate), "-f", "s16le", "pipe:1")
		if err != nil {
			return nil, refuse(503, CodeMediaGone, "no audio")
		}
		return binary(out), nil
	}
	s.routes["media/info"] = func(c *call) (any, *Refusal) {
		var in struct {
			Video string `json:"video"`
		}
		if ref := c.decode(&in); ref != nil {
			return nil, ref
		}
		if ref := c.readScope(in.Video); ref != nil {
			return nil, ref
		}
		var dur sql.NullFloat64
		var info sql.NullString
		err := c.s.DB.QueryRowContext(c.ctx, `SELECT v.duration_sec, CAST(to_jsonb(mi) - 'video_id' AS text) FROM videos v
			LEFT JOIN media_info mi ON mi.video_id = v.id WHERE v.id = $1`, in.Video).Scan(&dur, &info)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, dbErr(err)
		}
		return map[string]any{"duration_sec": dur.Float64, "media_info": rawOrNull(info)}, nil
	}
}

func rawOrNull(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return jsonText(s.String)
}

// jsonText is JSON text sent as itself.
type jsonText string

// MarshalJSON writes the text as it is.
func (j jsonText) MarshalJSON() ([]byte, error) { return []byte(j), nil }
