package channel

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// TSRunner streams a continuous MPEG-TS mux of a channel to `w` (D9). The
// production runner pipes ffmpeg's stdout; tests inject a fake that writes
// canned bytes. It blocks until the context is cancelled or the consumer
// disconnects (the HDHomeRun tuner closing its HTTP connection).
type TSRunner interface {
	StreamMPEGTS(ctx context.Context, concatPath string, w io.Writer) error
}

// StreamChannelTS streams the channel as MPEG-TS using the engine's
// configured TS runner — the convenience the HDHomeRun tuner handler
// (Story 27.5) calls. Satisfies hdhr's ChannelStreamer interface.
func (e *Engine) StreamChannelTS(ctx context.Context, channelID uuid.UUID, w io.Writer) error {
	return e.ServeMPEGTS(ctx, channelID, e.TS, w)
}

// ServeMPEGTS writes a continuous MPEG-TS stream for `channelID`, joined
// at the current wall clock, to `w` (the HDHomeRun tuner response). It
// reuses the same join + concat as the HLS path, swapping only the mux
// (Story 27.5 D2). Returns ErrNoProgram when the channel has nothing on
// air. Unlike Tune it does not enter the warm registry — an HDHomeRun
// pull is leased per-connection by Story 27.5.
func (e *Engine) ServeMPEGTS(ctx context.Context, channelID uuid.UUID, ts TSRunner, w io.Writer) error {
	if ts == nil {
		return errors.New("channel: no MPEG-TS runner configured")
	}
	now := e.now()
	blocks, err := e.Repo.ProgramsFrom(ctx, channelID, now, e.Lookahead+1)
	if err != nil {
		return err
	}
	join, err := Locate(blocks, now)
	if err != nil {
		return err
	}
	entries := BuildConcat(blocks, join, e.Lookahead)
	if len(entries) == 0 {
		return errors.New("channel: no resolvable program files at join point")
	}
	outDir := e.Layout.HLSDir(channelSessionID(channelID))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	concatPath := filepath.Join(outDir, "concat-ts.ffconcat")
	if err := os.WriteFile(concatPath, []byte(FormatConcat(entries)), 0o644); err != nil {
		return err
	}
	return ts.StreamMPEGTS(ctx, concatPath, w)
}
