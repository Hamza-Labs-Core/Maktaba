package channel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// A programme whose file cannot be read (README dizqueTV parity, row 5):
// missing at its mapped path, refused, answering too slowly or stalling
// mid-read. The feeder airs the channel's filler in its place (feeder.go,
// standIn) instead of an hour of slate, the timeline stays on the wall clock,
// and the file is handed to the probe stage: a file the probe cannot read
// either is a failed probe, which repair_broken picks up.

// ErrSourceUnreadable marks a block whose file cannot be read.
var ErrSourceUnreadable = errors.New("channel: source file unreadable")

// ErrSourceTimeout (with ErrSourceUnreadable) is a file whose share did not
// answer within the source timeout.
var ErrSourceTimeout = errors.New("no answer")

// FillerSource is implemented by repos that list a channel's filler: the
// stand-ins for a block whose file cannot be read.
type FillerSource interface {
	// FillersAround returns up to limit filler and bumper blocks of the
	// channel's schedule (the generation it airs) that have a file, one per
	// video, nearest `at` first: the planner puts a slot's own filler next
	// to its programmes.
	FillersAround(ctx context.Context, channelID uuid.UUID, at time.Time, limit int) ([]ProgramBlock, error)
}

// UnreadableReporter is implemented by repos that report a file that cannot
// be read.
type UnreadableReporter interface {
	ReportUnreadable(ctx context.Context, videoID uuid.UUID, why string) error
}

// statFile is os.Stat (tests stand in a share that does not answer).
var statFile = os.Stat

// fillerCandidates is how many filler blocks a stand-in is chosen from.
const fillerCandidates = 12

// sourceTimeout bounds the check and the probe of a file.
func (s *Station) sourceTimeout() time.Duration {
	if s.cfg.SourceTimeout > 0 {
		return s.cfg.SourceTimeout
	}
	return DefaultSourceTimeout
}

// checkSource tells whether the block's file is there before it is started
// (the feeder's Check). A share that does not answer within the source
// timeout counts as unreadable: the channel moves on, the stat is left to
// return on its own.
func (s *Station) checkSource(ctx context.Context, b ProgramBlock) error {
	p := s.cfg.PathMap.Apply(b.Path)
	if _, err := LocalInput(p); err != nil {
		return fmt.Errorf("%w: %v", ErrSourceUnreadable, err)
	}
	return statWithin(ctx, p, s.sourceTimeout())
}

// statWithin stats p, giving up after d.
func statWithin(ctx context.Context, p string, d time.Duration) error {
	res := make(chan error, 1)
	stat := statFile
	go func() {
		fi, err := stat(p)
		if err == nil && fi.IsDir() {
			err = errors.New("is a directory")
		}
		res <- err
	}()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case err := <-res:
		if err != nil {
			return fmt.Errorf("%w: %v", ErrSourceUnreadable, err)
		}
		return nil
	case <-t.C:
		return fmt.Errorf("%w: %s: %w within %s", ErrSourceUnreadable, p, ErrSourceTimeout, d)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// reportUnreadable logs a block whose file cannot be read and, when the rules
// say so, asks the probe stage to read the video again (the feeder's
// OnUnreadable, once per block). It never waits for the database.
func (s *Station) reportUnreadable(b ProgramBlock, err error) {
	s.log.Warn("channel: file unreadable, filler airs in its place", "seq", b.Seq, "kind", b.Kind,
		"video_id", b.VideoID.String(), "path", b.Path, "err", errString(err), "event", "channel_source_unreadable")
	r, ok := s.cfg.Repo.(UnreadableReporter)
	if !ok || !s.cfg.ReportUnreadable || b.VideoID == uuid.Nil {
		return
	}
	go func() {
		ctx, cancel := dbCtx(s.ctx)
		defer cancel()
		if err := r.ReportUnreadable(ctx, b.VideoID, errString(err)); err != nil && s.ctx.Err() == nil {
			s.log.Warn("channel: unreadable file not reported", "video_id", b.VideoID.String(),
				"err", err.Error(), "event", "channel_source_report_failed")
		}
	}()
}
