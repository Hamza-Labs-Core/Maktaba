package ffmpeg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Remuxer drives Story 8.4's cache-then-stream remux. It owns the
// "compute output path → spawn ffmpeg → atomic rename → return path"
// flow; the HTTP handler then range-serves the result via the direct
// play codepath.
type Remuxer struct {
	Bin       Binary
	OutputDir string // typically cache/{TierRemux}
}

// Plan describes one remux job.
type Plan struct {
	InputPath string
	Hash      string // SHA-256 of source bytes — keys the output filename
}

// OutputPath returns where the remux output will live (or already lives).
func (r *Remuxer) OutputPath(p Plan) string {
	if len(p.Hash) < 2 {
		return filepath.Join(r.OutputDir, "00", p.Hash+".mp4")
	}
	return filepath.Join(r.OutputDir, p.Hash[:2], p.Hash+".mp4")
}

// Run produces the remuxed file. If the file already exists, returns
// its path immediately (cache hit, AC-2). Otherwise spawns ffmpeg with
// `-c copy`, writes to a tempfile, and renames atomically — partial
// readers never see a half-written file.
//
// Returns the final on-disk path. Caller is responsible for serving
// it via the direct-play handler (Story 8.4 AC-1).
func (r *Remuxer) Run(ctx context.Context, plan Plan) (string, error) {
	out := r.OutputPath(plan)
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if plan.InputPath == "" {
		return "", errors.New("remux: empty input path")
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	tmp := out + ".part"
	args := RemuxArgs(plan.InputPath, tmp)
	proc, err := Spawn(ctx, r.Bin, args)
	if err != nil {
		return "", err
	}
	if err := proc.Wait(); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return out, nil
}
