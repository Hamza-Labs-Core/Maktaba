//! `mkp keys`: the developer's signing key (story 54.14 §1, 54.12). Ed25519; the private
//! half never leaves this machine. Keys live in $MKP_KEYS_DIR, or ~/.maktaba/keys:
//!
//!   <id>.key   the 32-byte seed in base64, readable by the owner only (0600)
//!   <id>.pub   {"id", "public"}: what `mkp keys export-public` prints for the console
//!
//! The id is the first 16 bytes of the sha256 of the public key, in hex (the home's rule),
//! so a developer.sig names exactly one key.

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine;
use maktaba_plugin_sig::Signer;
use std::path::{Path, PathBuf};

pub fn dir() -> PathBuf {
    if let Some(d) = std::env::var_os("MKP_KEYS_DIR") {
        return PathBuf::from(d);
    }
    let home = std::env::var_os("HOME").or_else(|| std::env::var_os("USERPROFILE")).unwrap_or_default();
    PathBuf::from(home).join(".maktaba").join("keys")
}

fn public_json(s: &Signer) -> String {
    serde_json::json!({"id": s.id, "public": s.public()}).to_string()
}

/// `mkp keys new`: a new key; prints its id (never the private key).
pub fn new_key() -> Result<(), String> {
    let mut seed = [0u8; 32];
    getrandom::getrandom(&mut seed).map_err(|e| format!("no randomness: {e}"))?;
    let s = Signer::from_seed(&seed);
    let d = dir();
    std::fs::create_dir_all(&d).map_err(|e| format!("{}: {e}", d.display()))?;
    let key = d.join(format!("{}.key", s.id));
    write_private(&key, B64.encode(seed).as_bytes())?;
    seed.fill(0);
    std::fs::write(d.join(format!("{}.pub", s.id)), public_json(&s) + "\n").map_err(|e| e.to_string())?;
    println!("{} (private key in {}; keep it, and a backup, off any shared machine)", s.id, key.display());
    Ok(())
}

#[cfg(unix)]
fn write_private(path: &Path, body: &[u8]) -> Result<(), String> {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let mut f = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
        .map_err(|e| format!("{}: {e}", path.display()))?;
    f.write_all(body).map_err(|e| e.to_string())
}

#[cfg(not(unix))]
fn write_private(path: &Path, body: &[u8]) -> Result<(), String> {
    std::fs::write(path, body).map_err(|e| format!("{}: {e}", path.display()))
}

/// A key by its id (in the keys folder) or by a path to its .key file.
pub fn load(which: &str) -> Result<Signer, String> {
    let p = if which.ends_with(".key") || which.contains('/') || which.contains('\\') {
        PathBuf::from(which)
    } else {
        dir().join(format!("{which}.key"))
    };
    let text = std::fs::read_to_string(&p).map_err(|e| format!("{}: {e}", p.display()))?;
    let raw = B64.decode(text.trim()).map_err(|_| format!("{}: not a key file", p.display()))?;
    let seed: [u8; 32] = raw.as_slice().try_into().map_err(|_| format!("{}: not a key file", p.display()))?;
    Ok(Signer::from_seed(&seed))
}

/// `mkp keys export-public [id]`: the public half for the console (the only key, if no id).
pub fn export_public(which: Option<&str>) -> Result<(), String> {
    let id = match which {
        Some(w) => w.to_string(),
        None => {
            let ids = list_ids()?;
            match ids.as_slice() {
                [one] => one.clone(),
                [] => return Err("no key yet: mkp keys new".into()),
                _ => return Err(format!("several keys ({}): name one", ids.join(", "))),
            }
        }
    };
    println!("{}", public_json(&load(&id)?));
    Ok(())
}

fn list_ids() -> Result<Vec<String>, String> {
    let mut ids: Vec<String> = match std::fs::read_dir(dir()) {
        Ok(rd) => rd
            .flatten()
            .filter_map(|e| e.file_name().to_string_lossy().strip_suffix(".key").map(str::to_string))
            .collect(),
        Err(_) => Vec::new(),
    };
    ids.sort();
    Ok(ids)
}

/// `mkp keys list`.
pub fn list() -> Result<(), String> {
    for id in list_ids()? {
        println!("{id}");
    }
    Ok(())
}
