//! Per-job limits (story 54.02 §5, README §4.7): what the dispatcher asks, clamped to the
//! manifest's resources and the README's maxima. A dispatcher can lower a limit, never
//! raise one.

use maktaba_plugin_manifest as manifest;
use serde_json::{Map, Value};

use crate::protocol::Limits;

pub const MAX_MEMORY_MB: u64 = 1024;
pub const MAX_TIMEOUT_S: u64 = 1800;
pub const MAX_SCRATCH_MB: u64 = 512;
/// Fuel per 60 s of timeout when the manifest says "default" (54.02 §5).
pub const FUEL_PER_MINUTE: u64 = 2_000_000_000;

/// The limits a job runs with.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct Effective {
    pub memory_bytes: u64,
    pub timeout_ms: u64,
    pub fuel: u64,
    pub scratch_bytes: u64,
    pub concurrency: usize,
}

/// The manifest's resources, as the cap.
pub fn from_manifest(m: &Map<String, Value>) -> Effective {
    let r = &m["resources"];
    let n = |k: &str, d: u64| r.get(k).and_then(Value::as_u64).unwrap_or(d);
    let memory_mb = n("memory_mb", 256).min(MAX_MEMORY_MB);
    let timeout_s = n("timeout_s", 300).min(MAX_TIMEOUT_S);
    let fuel = match r.get("fuel") {
        Some(Value::Number(f)) => f.as_u64().unwrap_or(0),
        _ => 0,
    };
    let fuel = if fuel == 0 { default_fuel(timeout_s * 1000) } else { fuel };
    Effective {
        memory_bytes: memory_mb << 20,
        timeout_ms: timeout_s * 1000,
        fuel,
        scratch_bytes: n("scratch_mb", 0).min(MAX_SCRATCH_MB) << 20,
        concurrency: n("concurrency", 2).clamp(1, 8) as usize,
    }
}

pub fn default_fuel(timeout_ms: u64) -> u64 {
    (timeout_ms.max(1) as u128 * FUEL_PER_MINUTE as u128 / 60_000).max(1) as u64
}

/// Clamps what the dispatcher asked to the cap; answers whether anything was lowered from
/// the request (`limits_clamped`, logged). Zero in the request means "the cap".
pub fn clamp(cap: &Effective, asked: &Limits) -> (Effective, bool) {
    let mut out = *cap;
    let mut clamped = false;
    let mut pick = |asked: u64, cap: u64| -> u64 {
        if asked == 0 {
            cap
        } else if asked > cap {
            clamped = true;
            cap
        } else {
            asked
        }
    };
    out.memory_bytes = pick(asked.memory_mb << 20, cap.memory_bytes);
    out.timeout_ms = pick(asked.timeout_ms, cap.timeout_ms);
    out.scratch_bytes = pick(asked.scratch_mb << 20, cap.scratch_bytes);
    // fuel asked for explicitly is clamped to the cap; otherwise it follows a lowered
    // timeout (never above the cap)
    out.fuel = if asked.fuel != 0 {
        pick(asked.fuel, cap.fuel)
    } else if asked.timeout_ms != 0 && asked.timeout_ms < cap.timeout_ms {
        default_fuel(out.timeout_ms).min(cap.fuel)
    } else {
        cap.fuel
    };
    (out, clamped)
}

/// Re-exported so callers see the manifest crate's id rule.
pub fn valid_id(id: &str) -> bool {
    manifest::valid_id(id)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn cap() -> Effective {
        Effective {
            memory_bytes: 256 << 20,
            timeout_ms: 300_000,
            fuel: default_fuel(300_000),
            scratch_bytes: 0,
            concurrency: 2,
        }
    }

    /// T09: limits above the manifest are clamped and reported.
    #[test]
    fn dispatcher_cannot_raise_limits() {
        let (e, clamped) = clamp(&cap(), &Limits { memory_mb: 4096, timeout_ms: 0, fuel: 0, scratch_mb: 0 });
        assert!(clamped);
        assert_eq!(e.memory_bytes, 256 << 20);
        let (e, clamped) = clamp(&cap(), &Limits { memory_mb: 64, timeout_ms: 5_000, fuel: 0, scratch_mb: 0 });
        assert!(!clamped);
        assert_eq!(e.memory_bytes, 64 << 20);
        assert_eq!(e.timeout_ms, 5_000);
        assert_eq!(e.fuel, default_fuel(5_000));
        let (_, clamped) =
            clamp(&cap(), &Limits { memory_mb: 0, timeout_ms: 9_000_000, fuel: u64::MAX, scratch_mb: 1 });
        assert!(clamped);
    }

    #[test]
    fn manifest_maxima_hold() {
        let m: Map<String, Value> = serde_json::from_str(r#"{"resources":{"memory_mb":1024,"timeout_s":1800,"fuel":"default","scratch_mb":512,"data_mb":50,"concurrency":8}}"#).unwrap();
        let e = from_manifest(&m);
        assert_eq!(e.memory_bytes, 1 << 30);
        assert_eq!(e.timeout_ms, 1_800_000);
        assert_eq!(e.fuel, 60_000_000_000);
        assert_eq!(e.concurrency, 8);
    }
}
