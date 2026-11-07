#!/usr/bin/env bash
# Builds the store's WebAssembly module (plugind/crates/storewasm: the home's package
# reader, manifest validator and the re-pack with store.sig) into store/build/store.wasm,
# which the Workers import. Build output, never committed.
set -euo pipefail
here="$(cd "$(dirname "$0")/.." && pwd)"
cd "$here/../plugind"
cargo build --locked -p maktaba-store-wasm --target wasm32-unknown-unknown --release
mkdir -p "$here/build"
cp target/wasm32-unknown-unknown/release/maktaba_store_wasm.wasm "$here/build/store.wasm"
echo "built $here/build/store.wasm"
