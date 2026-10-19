package main

// Epic 35 story 35.8 (design §9.1, §11.5, R13): a full node's streaming role serves
// video on demand itself — the API on the same machine opens the sessions over gRPC,
// the bytes come from the NAS through this machine's mounts (the share-path resolver),
// never through the home — next to the channels the home's front door places on it.

import (
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/handlers"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/probe"
	"github.com/Hamza-Labs-Core/Maktaba/streaming/internal/session"
)

// fullNodeFromEnv: MAKTABA_FULL_NODE=1 (the node's supervisor sets it in node mode).
func fullNodeFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("MAKTABA_FULL_NODE")))
	return v == "1" || v == "true" || v == "yes"
}

// buildVOD is the Epic 08 byte-pumping surface alone (direct play, HLS/DASH sessions,
// subtitles, chapters): no HDHomeRun, guide or home TV (those are the home's).
func buildVOD(bg *background, logger *slog.Logger, backend probe.Backend, transcripts handlers.TranscriptStreamer, store session.Store) http.Handler {
	return buildPublicHandler(bg, logger, backend, transcripts, nil, store)
}

// splitFullNode sends the channel routes the front door forwards (signed) and the
// node's internal TV routes to the TV handler, everything else to VOD.
func splitFullNode(tv, vod http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/stream/channel/") || strings.HasPrefix(p, "/internal/") {
			tv.ServeHTTP(w, r)
			return
		}
		vod.ServeHTTP(w, r)
	})
}
