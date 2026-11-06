package main

import (
	"log/slog"
	"os"

	"github.com/Hamza-Labs-Core/Maktaba/shared/go/ports"
)

// offlineEnv is what MAKTABA_OFFLINE (#640, ports.Outbound) sets for the API: each
// outbound call the API makes by itself has its own switch, and offline turns every one
// of them off, so the code that reads them needs nothing new. The licence refresh is
// checked where it is built (licenseRefresher).
var offlineEnv = map[string]string{
	"MAKTABA_UPDATE_DISABLE": "1",   // the update check, the release download, Sigstore's TUF root
	"MAKTABA_TELEMETRY_URL":  "off", // usage data to Maktaba Cloud
}

// applyOffline sets offlineEnv when MAKTABA_OFFLINE is on, and says so once.
func applyOffline(logger *slog.Logger) {
	if !ports.Offline() {
		return
	}
	for k, v := range offlineEnv {
		_ = os.Setenv(k, v)
	}
	logger.Info("offline: the API makes no outbound call of its own (updates, telemetry, licence refresh)",
		"event", "offline_mode")
}
