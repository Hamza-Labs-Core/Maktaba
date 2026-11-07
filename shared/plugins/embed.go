// Package plugins holds the files every reader of a Maktaba plugin shares (Epic 54,
// story 54.01 §3): the manifest schema, the capability and event lists and the WIT world.
// Go embeds them from here; Rust include_str!s them; the pipeline and the TypeScript SDK
// keep checked copies (make plugins-sync).
package plugins

import _ "embed"

// ManifestSchema is manifest.schema.json.
//
//go:embed manifest.schema.json
var ManifestSchema []byte

// Capabilities is capabilities.toml.
//
//go:embed capabilities.toml
var Capabilities []byte

// Events is events.toml.
//
//go:embed events.toml
var Events []byte

// WIT is the maktaba:plugin world.
//
//go:embed wit/maktaba-plugin.wit
var WIT []byte

// PluginsRoot is plugins-root.json, the plugin root keys every home trusts (story 54.14
// §1). Until the owner makes the two offline root keys it holds none, and nothing a store
// signed verifies; test builds (-tags e2etrust) read MAKTABA_E2E_PLUGINS_ROOT instead
// (pluginsig.EmbeddedRoot).
//
//go:embed plugins-root.json
var PluginsRoot []byte

// RevokedEmbedded is the revocation list as of this release's build (54.14 §4), "null"
// when the release carries none. StoreKeysEmbedded is store-keys.json as of the build, so a
// home that never reached the store can still verify a store-signed file (README §6.4).
//
//go:embed revoked-embedded.json
var RevokedEmbedded []byte

//go:embed store-keys-embedded.json
var StoreKeysEmbedded []byte

// FlowSchema is flow.schema.json (story 54.39 §1).
//
//go:embed flow.schema.json
var FlowSchema []byte

// FlowMessages is flow-messages.json: the flow check's codes in English and Arabic.
//
//go:embed flow-messages.json
var FlowMessages []byte
