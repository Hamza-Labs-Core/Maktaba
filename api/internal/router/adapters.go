package router

import (
	"context"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/pipeline"
	grpcstreaming "github.com/Hamza-Labs-Core/Maktaba/api/internal/grpcclients/streaming"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/settings"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
)

// pipelineSettingsAdapter adapts the gRPC pipeline.Client interface
// into the settings.PipelineSettingsClient surface. We keep the two
// interfaces separate so the settings package doesn't import grpcclients
// directly.
type pipelineSettingsAdapter struct {
	client pipeline.Client
}

func (a pipelineSettingsAdapter) ListBackends(ctx context.Context) ([]settings.Backend, error) {
	if a.client == nil {
		return nil, nil
	}
	b, err := a.client.ListBackends(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]settings.Backend, 0, len(b))
	for _, x := range b {
		cost := x.CostPerMinuteUSD
		var costPtr *float64
		if cost != 0 {
			costPtr = &cost
		}
		out = append(out, settings.Backend{
			Name:             x.Name,
			Available:        x.Available,
			Version:          x.Version,
			Models:           x.Models,
			HWAccel:          x.HWAccel,
			CostPerMinuteUSD: costPtr,
		})
	}
	return out, nil
}

func (a pipelineSettingsAdapter) STTTest(ctx context.Context, backend string, config map[string]any) (settings.STTTestResult, error) {
	if a.client == nil {
		return settings.STTTestResult{}, nil
	}
	res, err := a.client.STTTest(ctx, backend, config)
	if err != nil {
		return settings.STTTestResult{}, err
	}
	// res is `any` from the gRPC wrapper — handle the common
	// shape (map[string]any) and surface anything else as a generic
	// success result with the data field stringified.
	if m, ok := res.(map[string]any); ok {
		out := settings.STTTestResult{OK: true}
		// The JSON codec decodes numbers as float64, so accept both that
		// and a pre-coerced int64 for latency_ms.
		switch v := m["latency_ms"].(type) {
		case int64:
			out.LatencyMs = v
		case float64:
			out.LatencyMs = int64(v)
		}
		if v, ok := m["sample_text"].(string); ok {
			out.SampleText = v
		}
		// The pipeline reports a per-test failure as ok=false; honour
		// either an explicit error string or a false ok flag.
		if v, ok := m["error"].(string); ok && v != "" {
			out.Error = v
			out.OK = false
		}
		if v, ok := m["ok"].(bool); ok && !v {
			out.OK = false
		}
		return out, nil
	}
	return settings.STTTestResult{OK: true}, nil
}

// streamingServiceAdapter adapts grpcstreaming.Client to the handler's
// streaming.SessionService interface.
type streamingServiceAdapter struct {
	client grpcstreaming.Client
}

func (a streamingServiceAdapter) Open(ctx context.Context, req streaming.OpenSessionRequest) (streaming.OpenSessionResponse, error) {
	if a.client == nil {
		return streaming.OpenSessionResponse{}, nil
	}
	at := 0
	if req.AudioTrack != nil {
		at = *req.AudioTrack
	}
	resp, err := a.client.OpenSession(ctx, grpcstreaming.OpenSessionRequest{
		VideoID:        req.VideoID,
		ClientProfile:  req.ClientProfile,
		AudioTrack:     at,
		SubtitleTrack:  req.SubtitleTrack,
		StartSec:       req.StartSec,
		MaxBitrateKbps: req.MaxBitrateKbps,
		Format:         req.Format,
		BurnSubs:       req.BurnSubs,
		ForceTranscode: req.ForceTranscode,
		ForceSoftware:  req.ForceSoftware,
	})
	if err != nil {
		return streaming.OpenSessionResponse{}, err
	}
	return streaming.OpenSessionResponse{
		SessionID:   resp.SessionID,
		Mode:        resp.Mode,
		ManifestURL: resp.ManifestURL,
		DirectURL:   resp.DirectURL,
		ExpiresAt:   resp.ExpiresAt,
	}, nil
}

func (a streamingServiceAdapter) Close(ctx context.Context, sessionID string) error {
	if a.client == nil {
		return nil
	}
	return a.client.CloseSession(ctx, sessionID)
}

func (a streamingServiceAdapter) Capabilities(ctx context.Context) (streaming.Capabilities, error) {
	if a.client == nil {
		return streaming.Capabilities{}, nil
	}
	caps, err := a.client.GetCapabilities(ctx)
	if err != nil {
		return streaming.Capabilities{}, err
	}
	return streaming.Capabilities{
		Codecs:              caps.Codecs,
		HWAccel:             caps.HWAccel,
		MaxBitrateKbps:      caps.MaxBitrateKbps,
		SupportedContainers: caps.SupportedContainers,
		TranscodeSlots: streaming.Slots{
			Used:     caps.TranscodeSlots.Used,
			Capacity: caps.TranscodeSlots.Capacity,
		},
	}, nil
}
