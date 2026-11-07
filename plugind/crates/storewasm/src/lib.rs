//! What the plugin store's Workers need from the home's own rules (Epic 54, stories 54.15 and
//! 54.16), compiled to WebAssembly (`wasm32-unknown-unknown`) so a Cloudflare Worker reads a
//! package exactly as a home will:
//!
//! - `inspect`: read a `.mkplugin` in memory with the home's limits and entry rules (the same
//!   as `componentpack.UnpackPlugin`: ≤ 50 MB packed, ≤ 200 MB unpacked, ≤ 500 files, safe
//!   paths, regular files only, every file listed in `files.json` with its size and sha256,
//!   `manifest.toml` and `files.json` first) and validate its manifest with the home's
//!   validator (the Rust reader of shared/plugins, story 54.01). It answers JSON: the
//!   normalised manifest, files.json, the signatures and the files' list, or the refusal code.
//! - `repack`: the same package with `signatures/store.sig` set to the store's signature, so
//!   the file on the CDN carries both (story 54.14 §2). Every other entry keeps its bytes.
//!
//! The ABI is plain: `alloc`/`dealloc` for buffers in this module's memory, and each call takes
//! (pointer, length) pairs and answers a pointer to `[status u8][len u32 LE][bytes]`, status 0
//! for success and 1 for a refusal whose bytes are `{"code","detail"}`.

pub mod pack;

use std::alloc::{alloc as std_alloc, dealloc as std_dealloc, Layout};

/// A buffer of len bytes in this module's memory, for the caller to fill.
///
/// # Safety
/// The caller frees it with `dealloc` and the same length.
#[no_mangle]
pub unsafe extern "C" fn alloc(len: usize) -> *mut u8 {
    std_alloc(Layout::from_size_align(len.max(1), 1).expect("a layout"))
}

/// Frees a buffer `alloc` gave.
///
/// # Safety
/// ptr and len are a buffer from `alloc`.
#[no_mangle]
pub unsafe extern "C" fn dealloc(ptr: *mut u8, len: usize) {
    std_dealloc(ptr, Layout::from_size_align(len.max(1), 1).expect("a layout"))
}

fn answer(result: Result<Vec<u8>, pack::Refusal>) -> *mut u8 {
    let (status, body) = match result {
        Ok(b) => (0u8, b),
        Err(r) => (1u8, serde_json::json!({"code": r.code, "detail": r.detail}).to_string().into_bytes()),
    };
    let mut out = Vec::with_capacity(body.len() + 5);
    out.push(status);
    out.extend_from_slice(&(body.len() as u32).to_le_bytes());
    out.extend_from_slice(&body);
    let len = out.len();
    // SAFETY: a fresh buffer of len bytes, filled from out
    unsafe {
        let p = alloc(len);
        std::ptr::copy_nonoverlapping(out.as_ptr(), p, len);
        p
    }
}

/// # Safety
/// (ptr, len) is a buffer of this module's memory.
unsafe fn slice<'a>(ptr: *const u8, len: usize) -> &'a [u8] {
    if len == 0 {
        return &[];
    }
    std::slice::from_raw_parts(ptr, len)
}

/// The answer's total length, for the caller to free it (5 + the body's length).
///
/// # Safety
/// ptr is an answer of this module.
#[no_mangle]
pub unsafe extern "C" fn answer_len(ptr: *const u8) -> usize {
    let mut n = [0u8; 4];
    std::ptr::copy_nonoverlapping(ptr.add(1), n.as_mut_ptr(), 4);
    5 + u32::from_le_bytes(n) as usize
}

/// Reads and checks a package (see the module's comment).
///
/// # Safety
/// (ptr, len) is a buffer of this module's memory.
#[no_mangle]
pub unsafe extern "C" fn inspect(ptr: *const u8, len: usize) -> *mut u8 {
    answer(pack::inspect(slice(ptr, len)).map(|i| i.to_json().into_bytes()))
}

/// A package from `{"files": {path: base64}, "sigs": {entry: text}}` (the store's tests).
///
/// # Safety
/// (ptr, len) is a buffer of this module's memory.
#[no_mangle]
pub unsafe extern "C" fn pack(ptr: *const u8, len: usize) -> *mut u8 {
    answer((|| {
        let v: serde_json::Value = serde_json::from_slice(slice(ptr, len))
            .map_err(|e| pack::Refusal { code: "bad_input".into(), detail: e.to_string() })?;
        let b64 = |s: &str| -> Result<Vec<u8>, pack::Refusal> {
            base64_decode(s).ok_or(pack::Refusal { code: "bad_input".into(), detail: "not base64".into() })
        };
        let mut files = std::collections::BTreeMap::new();
        for (k, x) in v["files"].as_object().into_iter().flatten() {
            files.insert(k.clone(), b64(x.as_str().unwrap_or(""))?);
        }
        let mut sigs = std::collections::BTreeMap::new();
        for (k, x) in v["sigs"].as_object().into_iter().flatten() {
            sigs.insert(k.clone(), x.as_str().unwrap_or("").as_bytes().to_vec());
        }
        pack::pack(&files, &sigs)
    })())
}

fn base64_decode(s: &str) -> Option<Vec<u8>> {
    let val = |c: u8| -> Option<u32> {
        Some(match c {
            b'A'..=b'Z' => c - b'A',
            b'a'..=b'z' => c - b'a' + 26,
            b'0'..=b'9' => c - b'0' + 52,
            b'+' => 62,
            b'/' => 63,
            _ => return None,
        } as u32)
    };
    let b = s.trim_end_matches('=').as_bytes();
    let mut out = Vec::with_capacity(b.len() * 3 / 4);
    let (mut acc, mut bits) = (0u32, 0);
    for &c in b {
        acc = (acc << 6) | val(c)?;
        bits += 6;
        if bits >= 8 {
            bits -= 8;
            out.push((acc >> bits) as u8);
            acc &= (1 << bits) - 1;
        }
    }
    Some(out)
}

/// The package with signatures/store.sig set to sig.
///
/// # Safety
/// Both pairs are buffers of this module's memory.
#[no_mangle]
pub unsafe extern "C" fn repack(ptr: *const u8, len: usize, sig: *const u8, sig_len: usize) -> *mut u8 {
    answer(pack::repack(slice(ptr, len), slice(sig, sig_len)))
}
