package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/broadcast"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/frontdoor"
)

// startBroadcast declares the home's broadcaster loops (Epic 50 story 50.22): one ladder
// and uploader per published channel (streaming:broadcast_uploader) and its control socket
// to the edge (streaming:broadcast_control). Both wait for slot 0226 and follow the
// livetv.broadcast component. Only the home's front door runs them, never a node.
func (b *tvBase) startBroadcast(fd *frontdoor.Handler) {
	var once sync.Once
	var ctl *broadcast.Ctl
	get := func() *broadcast.Ctl { // built on first use: nothing is held while the component is off
		once.Do(func() {
			ctl = &broadcast.Ctl{Store: broadcast.PGStore{Pool: b.pool}, Source: fd.RelaySource(), FFmpeg: b.cfg.FFmpeg.Binary,
				Dir: filepath.Join(b.layout.Root, "broadcast"), Logger: b.logger, Output: b.broadcastOutput}
		})
		return ctl
	}
	b.bg.Loop("broadcast_uploader", func(ctx context.Context) {
		if b.waitBroadcastSchema(ctx) {
			get().Run(ctx)
		}
	})
	b.bg.Loop("broadcast_control", func(ctx context.Context) {
		if b.waitBroadcastSchema(ctx) {
			(&broadcast.Controls{Ctl: get()}).Run(ctx)
		}
	})
}

func (b *tvBase) waitBroadcastSchema(ctx context.Context) bool {
	st := broadcast.PGStore{Pool: b.pool}
	for !st.Available(ctx) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(30 * time.Second):
		}
	}
	return true
}

// broadcastOutput is the channels' own encode and the resolved encoder family, with
// rules.broadcast.allow_software_rungs.
func (b *tvBase) broadcastOutput(ctx context.Context) broadcast.Output {
	out := b.settings.Output(ctx)
	enc := b.encoders.Resolve(ctx, out.Encoder)
	family := enc.Name
	if enc.VTSoftware {
		family = broadcast.EncSoftware
	}
	o := broadcast.Output{Encoder: family, Width: out.Width, Height: out.Height, FPS: out.FPS,
		KeyintSeconds: out.KeyintSeconds, VideoKbps: out.VideoKbps, AudioKbps: out.AudioKbps}
	var raw []byte
	qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if b.pool.QueryRow(qctx, `SELECT value::text FROM app_settings WHERE key = 'rules.broadcast'`).Scan(&raw) == nil {
		var r struct {
			AllowSoftwareRungs bool `json:"allow_software_rungs"`
		}
		if json.Unmarshal(raw, &r) == nil {
			o.AllowSoftware = r.AllowSoftwareRungs
		}
	}
	return o
}
