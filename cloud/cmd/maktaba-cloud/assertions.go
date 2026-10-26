package main

import (
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/config"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/entitlement"
	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/homeassert"
)

// assertionSigner loads the home-assertion key: derived from the
// entitlement key (entitlement.private_key_path), so the one key file the
// operator already backs up covers both. Nil (logged) when none is set.
func assertionSigner(logger *slog.Logger, cfg config.Config) *homeassert.Signer {
	path := cfg.Entitlement.PrivateKeyPath
	if path == "" {
		logger.Warn("no entitlement.private_key_path: linked-account sign-in (home assertions) is off")
		return nil
	}
	s, err := entitlement.LoadSignerFromFile(path)
	if err != nil {
		logger.Error("entitlement key unreadable: linked-account sign-in is off", "err", err)
		return nil
	}
	return homeassert.DeriveFromSeed(s.Priv.Seed())
}
