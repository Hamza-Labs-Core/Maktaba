package log

import (
	"bufio"
	"context"
	"io"
	"log/slog"
	"strings"
)

// WrapFFmpegStderr reads FFmpeg's stderr line-by-line and emits a
// structured log record per line (EC2). The raw line is preserved in
// the `line` field; the level is mapped from FFmpeg's own bracketed
// prefix where it is present.
//
// This funcion blocks until r returns EOF; callers run it in a
// dedicated goroutine alongside the FFmpeg subprocess.
func WrapFFmpegStderr(ctx context.Context, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		level := slog.LevelInfo
		switch {
		case strings.HasPrefix(line, "[error]"), strings.HasPrefix(line, "[fatal]"):
			level = slog.LevelError
		case strings.HasPrefix(line, "[warning]"):
			level = slog.LevelWarn
		case strings.HasPrefix(line, "[debug]"), strings.HasPrefix(line, "[trace]"):
			level = slog.LevelDebug
		}
		From(ctx).Log(ctx, level, "ffmpeg",
			"event", "ffmpeg_stderr",
			"line", line,
		)
	}
}
