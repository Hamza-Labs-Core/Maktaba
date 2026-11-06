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
