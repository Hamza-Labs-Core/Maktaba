package pluginsig

import (
	"bytes"

	plugins "github.com/Hamza-Labs-Core/Maktaba/shared/plugins"
)

// EmbeddedRoot is the root this binary trusts: shared/plugins/plugins-root.json, or in a
// test build (-tags e2etrust) the file MAKTABA_E2E_PLUGINS_ROOT names. A release build
// never reads that variable (root_noe2e.go).
func EmbeddedRoot() (*Root, error) {
	if raw, ok := e2eRoot(); ok {
		return ParseRoot(raw)
	}
	return ParseRoot(plugins.PluginsRoot)
}

// embeddedOrNil is an embedded list, nil when the release carries none ("null").
func embeddedOrNil(b []byte) []byte {
	t := bytes.TrimSpace(b)
	if len(t) == 0 || bytes.Equal(t, []byte("null")) {
		return nil
	}
	return t
}

// EmbeddedStoreKeys is store-keys.json as of this release's build (nil: none).
func EmbeddedStoreKeys() []byte {
	if raw, ok := e2eFile("MAKTABA_E2E_PLUGINS_STORE_KEYS"); ok {
		return raw
	}
	return embeddedOrNil(plugins.StoreKeysEmbedded)
}

// EmbeddedRevoked is the revocation list as of this release's build (nil: none).
func EmbeddedRevoked() []byte {
	if raw, ok := e2eFile("MAKTABA_E2E_PLUGINS_REVOKED"); ok {
		return raw
	}
	return embeddedOrNil(plugins.RevokedEmbedded)
}
