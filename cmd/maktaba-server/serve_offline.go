package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/cmd/maktaba-server/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// Offline (#640): `serve --offline`, or MAKTABA_OFFLINE=1 in its environment, stops every
// outbound call the server and its roles make by themselves (ports.Outbound, By "auto"):
// a test stack, a branch's scratch stack, or a home with no internet. Each call keeps its
// own switch; offline sets all of them, and every role inherits the environment.

// offlineEnv is what the roles get while offline.
var offlineEnv = map[string]string{
	ports.EnvOffline:         "1",   // the API's own switch (api/offline.go)
	"MAKTABA_UPDATE_DISABLE": "1",   // the update check, downloads and self-update
	"MAKTABA_TELEMETRY_URL":  "off", // usage data
	"HF_HUB_OFFLINE":         "1",   // the pipeline's Hugging Face models: the cache only
	"TRANSFORMERS_OFFLINE":   "1",
}

// applyOffline turns cfg's outbound switches off when offline (flag, or the environment)
// and sets offlineEnv for this process and its roles. It answers whether it did.
func applyOffline(cfg *config.Config, flag bool, log io.Writer) bool {
	if flag {
		_ = os.Setenv(ports.EnvOffline, "1")
	}
	if !ports.Offline() {
		return false
	}
	for k, v := range offlineEnv {
		_ = os.Setenv(k, v)
	}
	cfg.Cloud.Enabled = false     // no relay agent: no link to Maktaba Cloud
	cfg.Components.Packs = "off"  // no pack or model download (packs_disabled)
	cfg.LLM.Pull = "off"          // the bundled Ollama pulls no model
	cfg.Telemetry.Enabled = "off" // MAKTABA_TELEMETRY_ENABLED=false for the API
	if log != nil {
		fmt.Fprintln(log, "[serve] offline: no outbound call (updates, packs, models, telemetry, the cloud link)")
	}
	return true
}
