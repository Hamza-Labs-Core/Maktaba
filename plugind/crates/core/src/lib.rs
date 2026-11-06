//! plugind-core: the Maktaba plugin host as a library (Epic 54, story 54.02), so the
//! `maktaba-plugind` binary and the SDK's test harness (54.12) run the same code.
//!
//! A job runs as one WebAssembly component in a fresh Wasmtime store, with only the
//! `maktaba:plugin` imports and a small WASI subset linked: no sockets, no HTTP, no
//! filesystem, no stdin, no processes (README §5.2). Every import forwards to the plugin
//! API on a unix socket, which decides.

pub mod api;
pub mod audit;
pub mod engine;
pub mod host;
pub mod limits;
pub mod logging;
pub mod protocol;
pub mod serve;
pub mod uds;

pub use engine::{Host, HostConfig};
