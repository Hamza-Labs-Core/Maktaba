package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/keys"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/signedurl"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/streaming"
)

// streamURLSigner is the signer of the stream URLs a session hands the player
// (Story 10.8): the machine's stream signing key (MAKTABA_STREAM_SIGNING_KEY,
// created on first use; the supervisor points the API and the streaming role
// at the same file), else the API's JWT keys (a deployment whose streaming
// service reads the API's JWKS). Nil when neither exists: the URLs then go out
// unsigned and the streaming service refuses them, which is logged loudly.
func streamURLSigner(authKeys *keys.Set, logger *slog.Logger) streaming.URLSigner {
	if path := strings.TrimSpace(os.Getenv(signedurl.EnvKeyFile)); path != "" {
		k, created, err := signedurl.LoadOrCreateKey(path)
		if err != nil {
			logger.Error("stream signing key unusable: video playback will be refused",
				"path", path, "err", err.Error(), "event", "stream_signing_key_failed")
			return nil
		}
		logger.Info("stream URLs signed with this machine's stream key", "kid", k.KID, "created", created,
			"event", "stream_signing_key_loaded")
		return signedurl.FromKey(k)
	}
	if authKeys != nil && authKeys.Active() != nil {
		logger.Info("stream URLs signed with the API's JWT key", "kid", authKeys.Active().KID,
			"event", "stream_signing_jwt_key")
		return signedurl.New(authKeys)
	}
	logger.Warn("no stream signing key and no JWT keys: video playback will be refused",
		"env", signedurl.EnvKeyFile, "event", "stream_signing_missing")
	return nil
}
