// Epic 16 Story 16.8 wiring: feature-flag resolution (/api/me/flags,
// /api/admin/flags, cohorts) and the server's long-term Ed25519
// identity key (Story 10.18) it signs the bundles with.
package router

import (
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/auth/serverkeys"
	flagspkg "github.com/Hamza-Labs-Core/Maktaba/api/internal/flags"
	flagsh "github.com/Hamza-Labs-Core/Maktaba/api/internal/handlers/flags"
	"github.com/Hamza-Labs-Core/Maktaba/api/internal/secrets"
)

// FlagsDeps bundles the flag surface's dependencies.
type FlagsDeps struct {
	DB *sql.DB
	// Entitlements is main.go's one entitlement store (EntitlementStore):
	// the paid flags are its Allows answers.
	Entitlements flagspkg.Entitlements
	// Identity signs the bundles. Nil: the routes answer 503.
	Identity *serverkeys.Store
	Logger   *slog.Logger
}

// MountFlags attaches the flag routes. A nil DB or entitlement store
// mounts nothing.
func MountFlags(r chi.Router, d FlagsDeps) {
	if d.DB == nil || d.Entitlements == nil {
		return
	}
	flagsh.New(d.DB, d.Entitlements, d.Identity, d.Logger).Mount(r)
}

// identitySealer seals the identity key's private PEM with the home's
// secrets key (the same key home_secrets uses): nonce || ciphertext.
type identitySealer struct{ key *secrets.Key }

const identitySealName = "server-identity"

func (s identitySealer) Seal(plain []byte) ([]byte, error) {
	ct, nonce, err := secrets.Seal(s.key, identitySealName, plain)
	if err != nil {
		return nil, err
	}
	return append(nonce, ct...), nil
}

func (s identitySealer) Open(sealed []byte) ([]byte, error) {
	const nonceSize = 12 // AES-GCM's
	if len(sealed) < nonceSize {
		return nil, errors.New("identity: sealed key too short")
	}
	return secrets.Open(s.key, identitySealName, sealed[nonceSize:], sealed[:nonceSize])
}

// LoadServerIdentity loads the server's Ed25519 identity (Story 10.18):
// MAKTABA_SERVER_IDENTITY_PRIVATE_PEM when set, else the sealed key
// under MAKTABA_STATE_DIR/identity (default: an identity folder beside
// the secrets key file), made on first start. A key that cannot be
// loaded leaves a key for this process only, with a warning: bundles
// stay signed, but a client that kept one from before the restart
// fetches the keys again (/api/me/flags/keys).
func LoadServerIdentity(sealKey *secrets.Key, logger *slog.Logger) *serverkeys.Store {
	if logger == nil {
		logger = slog.Default()
	}
	opts := serverkeys.LoadOptions{EnvPEM: os.Getenv("MAKTABA_SERVER_IDENTITY_PRIVATE_PEM")}
	if dir := strings.TrimSpace(os.Getenv("MAKTABA_STATE_DIR")); dir != "" {
		opts.DiskDir = filepath.Join(dir, "identity")
	} else {
		opts.DiskDir = filepath.Join(filepath.Dir(secrets.KeyPath(os.Getenv)), "identity")
	}
	if sealKey != nil {
		opts.Sealer = identitySealer{key: sealKey}
	}
	key, fresh, err := serverkeys.Load(opts)
	if err != nil {
		logger.Warn("p16: server identity key unavailable; signing with a key for this process only",
			"err", err, "event", "server_identity_ephemeral")
		if key, err = serverkeys.Generate(time.Now(), "generated"); err != nil {
			logger.Error("p16: no server identity key; flag bundles disabled", "err", err,
				"event", "server_identity_failed")
			return nil
		}
	} else if fresh {
		logger.Info("p16: server identity key created", "kid", key.Kid, "dir", opts.DiskDir,
			"event", "server_identity_created")
	}
	store, err := serverkeys.NewStore(key, 0, nil)
	if err != nil {
		logger.Error("p16: server identity store", "err", err, "event", "server_identity_failed")
		return nil
	}
	return store
}
