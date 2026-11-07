//! The plugin chain of trust (Epic 54, story 54.14) in Rust, for the SDK and its harness:
//! the same rules as the home's Go verifier (shared/plugins/pluginsig), checked on the
//! shared vectors (shared/plugins/testdata/signatures/). A signature is Ed25519 over
//! `"maktaba-plugins/v1/" + kind + "\n" + canonical(signed)`; a key id is the first 16 bytes
//! of the sha256 of the raw public key, in hex.

pub mod canonical;

use base64::engine::general_purpose::STANDARD as B64;
use base64::Engine;
use ed25519_dalek::{Signature as EdSig, Signer as _, SigningKey, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeMap;

pub const CODE_SIGNATURE_INVALID: &str = "plugin_signature_invalid";
pub const CODE_DEVELOPER_KEY_MISMATCH: &str = "developer_key_mismatch";
pub const CODE_KEY_USE_MISMATCH: &str = "key_use_mismatch";
pub const CODE_STORE_KEYS_ROLLBACK: &str = "store_keys_rollback_refused";
pub const CODE_STORE_KEYS_EXPIRED: &str = "store_keys_expired";
pub const CODE_REVOCATION_ROLLBACK: &str = "revocation_rollback_refused";
pub const CODE_REVOCATION_EXPIRED: &str = "revocation_expired";
pub const CODE_CATALOG_ROLLBACK: &str = "catalog_rollback_refused";
pub const CODE_CATALOG_EXPIRED: &str = "catalog_expired";
pub const CODE_ENTITLEMENT_INVALID: &str = "entitlement_invalid";

/// A refusal: its code, the step of the chain, and why.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Error {
    pub code: &'static str,
    pub step: &'static str,
    pub msg: String,
}

impl std::fmt::Display for Error {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}: {}: {}", self.code, self.step, self.msg)
    }
}

impl std::error::Error for Error {}

fn fail<T>(code: &'static str, step: &'static str, msg: impl Into<String>) -> Result<T, Error> {
    Err(Error { code, step, msg: msg.into() })
}

/// The id of a raw public key.
pub fn key_id(public: &[u8]) -> String {
    hex::encode(&Sha256::digest(public)[..16])
}

/// What a signature of kind covers.
pub fn message(kind: &str, signed: &[u8]) -> Result<Vec<u8>, canonical::CanonicalError> {
    let mut m = format!("maktaba-plugins/v1/{kind}\n").into_bytes();
    m.extend(canonical::canonical(signed)?);
    Ok(m)
}

fn decode_key(id: &str, public: &str) -> Result<VerifyingKey, String> {
    let raw = B64.decode(public).map_err(|_| format!("key {id}: not base64"))?;
    let arr: [u8; 32] = raw.as_slice().try_into().map_err(|_| format!("key {id}: not an Ed25519 public key"))?;
    if key_id(&arr) != id {
        return Err(format!("key {id}: the id does not name its public key"));
    }
    VerifyingKey::from_bytes(&arr).map_err(|_| format!("key {id}: not an Ed25519 public key"))
}

fn verify_sig(key: &VerifyingKey, kind: &str, signed: &[u8], sig: &str) -> bool {
    let Ok(s) = B64.decode(sig) else { return false };
    let Ok(arr) = <[u8; 64]>::try_from(s.as_slice()) else { return false };
    let Ok(msg) = message(kind, signed) else { return false };
    key.verify_strict(&msg, &EdSig::from_bytes(&arr)).is_ok()
}

/// Seconds since 1970 of an RFC 3339 time ("2026-11-06T12:00:00Z", a fraction and an
/// offset allowed).
pub fn parse_time(s: &str) -> Option<i64> {
    let b = s.as_bytes();
    if b.len() < 20 || b[4] != b'-' || b[7] != b'-' || !matches!(b[10], b'T' | b't') || b[13] != b':' || b[16] != b':' {
        return None;
    }
    let num = |r: std::ops::Range<usize>| -> Option<i64> { std::str::from_utf8(&b[r]).ok()?.parse().ok() };
    let (y, mo, d, h, mi, se) = (num(0..4)?, num(5..7)?, num(8..10)?, num(11..13)?, num(14..16)?, num(17..19)?);
    let mut rest = &s[19..];
    if let Some(r) = rest.strip_prefix('.') {
        let n = r.bytes().take_while(|c| c.is_ascii_digit()).count();
        if n == 0 {
            return None;
        }
        rest = &r[n..];
    }
    let off = match rest {
        "Z" | "z" => 0,
        _ if rest.len() == 6 && (rest.starts_with('+') || rest.starts_with('-')) && &rest[3..4] == ":" => {
            let oh: i64 = rest[1..3].parse().ok()?;
            let om: i64 = rest[4..6].parse().ok()?;
            let o = oh * 3600 + om * 60;
            if rest.starts_with('-') {
                -o
            } else {
                o
            }
        }
        _ => return None,
    };
    if !(1..=12).contains(&mo) || !(1..=31).contains(&d) || h > 23 || mi > 59 || se > 60 {
        return None;
    }
    // days from the civil date (Howard Hinnant's algorithm)
    let yy = if mo <= 2 { y - 1 } else { y };
    let era = yy.div_euclid(400);
    let yoe = yy - era * 400;
    let mp = (mo + 9) % 12;
    let doy = (153 * mp + 2) / 5 + d - 1;
    let doe = yoe * 365 + yoe / 4 - yoe / 100 + doy;
    let days = era * 146097 + doe - 719468;
    Some(days * 86400 + h * 3600 + mi * 60 + se - off)
}

fn time_of(field: &str, s: &str, step: &'static str) -> Result<i64, Error> {
    parse_time(s)
        .map_or_else(|| fail(CODE_SIGNATURE_INVALID, step, format!("{field}: {s:?} is not an RFC 3339 time")), Ok)
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Sig {
    pub key_id: String,
    pub sig: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    signed: serde_json::Value,
    signatures: Vec<Sig>,
}

/// An envelope's signed object as canonical bytes, and its signatures.
struct Env {
    signed: Vec<u8>,
    sigs: Vec<Sig>,
}

fn envelope(raw: &[u8], step: &'static str) -> Result<Env, Error> {
    // strict reading first: a key twice or a fraction anywhere refuses the file
    canonical::parse(raw).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))?;
    let env: Envelope = serde_json::from_slice(raw).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))?;
    let signed = canonical::canonical_of(&env.signed).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))?;
    Ok(Env { signed, sigs: env.signatures })
}

fn strict<T: for<'de> Deserialize<'de>>(b: &[u8], step: &'static str) -> Result<T, Error> {
    serde_json::from_slice(b).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))
}

fn count_valid(env: &Env, kind: &str, keys: &BTreeMap<String, VerifyingKey>) -> usize {
    let mut seen = std::collections::BTreeSet::new();
    for s in &env.sigs {
        if seen.contains(&s.key_id) {
            continue;
        }
        if let Some(k) = keys.get(&s.key_id) {
            if verify_sig(k, kind, &env.signed, &s.sig) {
                seen.insert(s.key_id.clone());
            }
        }
    }
    seen.len()
}

// ---- the root and store-keys.json -------------------------------------------------------

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct RootKey {
    id: String,
    public: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct RootSigned {
    version: i64,
    threshold: usize,
    keys: Vec<RootKey>,
}

/// The newest versions a home has seen (54.14 §3), as Go's State.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct State {
    #[serde(default)]
    pub store_keys_version: i64,
    #[serde(default)]
    pub store_keys_sha256: String,
    #[serde(default)]
    pub revoked_version: i64,
    #[serde(default)]
    pub revoked_sha256: String,
    #[serde(default)]
    pub catalog_versions: BTreeMap<String, i64>,
    #[serde(default)]
    pub newest_issued: String,
}

impl State {
    /// The home's time: the newest of the clock and the newest issue time seen.
    pub fn now(&self, clock: i64) -> i64 {
        parse_time(&self.newest_issued).map_or(clock, |n| n.max(clock))
    }
}

pub struct Root {
    pub version: i64,
    pub threshold: usize,
    keys: BTreeMap<String, VerifyingKey>,
}

impl Root {
    pub fn parse(raw: &[u8]) -> Result<Root, Error> {
        let env = envelope(raw, "root")?;
        let s: RootSigned = strict(&env.signed, "root")?;
        let mut keys = BTreeMap::new();
        for k in s.keys {
            let pk = decode_key(&k.id, &k.public).or_else(|e| fail(CODE_SIGNATURE_INVALID, "root", e))?;
            keys.insert(k.id, pk);
        }
        if !keys.is_empty() && (s.threshold < 1 || s.threshold > keys.len()) {
            return fail(CODE_SIGNATURE_INVALID, "root", format!("threshold {}", s.threshold));
        }
        if !keys.is_empty() && count_valid(&env, "root", &keys) < s.threshold {
            return fail(CODE_SIGNATURE_INVALID, "root", "the root is not signed by its own keys");
        }
        Ok(Root { version: s.version, threshold: s.threshold, keys })
    }

    pub fn is_empty(&self) -> bool {
        self.keys.is_empty()
    }

    /// The next root, signed by this one's threshold (TUF's root chaining).
    pub fn verify_update(&self, next: &[u8]) -> Result<Root, Error> {
        let r = Root::parse(next)?;
        let env = envelope(next, "root")?;
        if r.version != self.version + 1 {
            return fail(
                CODE_SIGNATURE_INVALID,
                "root",
                format!("version {} does not follow {}", r.version, self.version),
            );
        }
        if count_valid(&env, "root", &self.keys) < self.threshold {
            return fail(CODE_SIGNATURE_INVALID, "root", "not signed by the previous root");
        }
        Ok(r)
    }

    pub fn verify_store_keys(&self, raw: &[u8], st: &State, clock: i64) -> Result<StoreKeys, Error> {
        if self.keys.is_empty() {
            return fail(CODE_SIGNATURE_INVALID, "root", "this build carries no plugin root keys");
        }
        let step = "store_keys";
        let env = envelope(raw, step)?;
        if count_valid(&env, "store-keys", &self.keys) < self.threshold {
            return fail(CODE_SIGNATURE_INVALID, step, "not signed by a key of the plugin root");
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Key {
            id: String,
            public: String,
            #[serde(rename = "use")]
            use_: String,
            not_before: String,
            not_after: String,
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Signed {
            version: i64,
            expires: String,
            keys: Vec<Key>,
        }
        let s: Signed = strict(&env.signed, step)?;
        let expires = time_of("expires", &s.expires, step)?;
        let sha = hex::encode(Sha256::digest(&env.signed));
        if s.version < st.store_keys_version {
            return fail(
                CODE_STORE_KEYS_ROLLBACK,
                step,
                format!("version {} is older than {}", s.version, st.store_keys_version),
            );
        }
        if s.version == st.store_keys_version && !st.store_keys_sha256.is_empty() && st.store_keys_sha256 != sha {
            return fail(
                CODE_STORE_KEYS_ROLLBACK,
                step,
                format!("version {} differs from the copy seen before", s.version),
            );
        }
        if st.now(clock) > expires {
            return fail(CODE_STORE_KEYS_EXPIRED, step, format!("expired {}", s.expires));
        }
        let mut keys = BTreeMap::new();
        for k in s.keys {
            let pk = decode_key(&k.id, &k.public).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e))?;
            if k.use_ != "store" && k.use_ != "entitlement" {
                return fail(CODE_SIGNATURE_INVALID, step, format!("key {}: use {}", k.id, k.use_));
            }
            let nb = time_of("not_before", &k.not_before, step)?;
            let na = time_of("not_after", &k.not_after, step)?;
            if keys.insert(k.id.clone(), StoreKey { key: pk, use_: k.use_, not_before: nb, not_after: na }).is_some() {
                return fail(CODE_SIGNATURE_INVALID, step, format!("key {} is listed twice", k.id));
            }
        }
        Ok(StoreKeys { version: s.version, expires, sha256: sha, keys })
    }
}

struct StoreKey {
    key: VerifyingKey,
    use_: String,
    not_before: i64,
    not_after: i64,
}

/// A verified store-keys.json.
pub struct StoreKeys {
    pub version: i64,
    pub expires: i64,
    pub sha256: String,
    keys: BTreeMap<String, StoreKey>,
}

/// A package to verify (files maps a files.json path to its bytes; empty skips the last step).
pub struct Package<'a> {
    pub files_json: &'a [u8],
    pub developer_sig: Option<&'a [u8]>,
    pub store_sig: Option<&'a [u8]>,
    pub plugin_id: &'a str,
    pub version: &'a str,
    pub capabilities_sha256: &'a str,
    pub files: Option<&'a BTreeMap<String, Vec<u8>>>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Verified {
    pub level: &'static str,
    pub developer_key_id: String,
    pub files_sha256: String,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct SigFile {
    key_id: String,
    #[serde(default)]
    public: Option<String>,
    sig: String,
    signed: serde_json::Value,
}

fn sig_file(raw: &[u8], step: &'static str) -> Result<(SigFile, Vec<u8>), Error> {
    canonical::parse(raw).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))?;
    let f: SigFile = strict(raw, step)?;
    let signed = canonical::canonical_of(&f.signed).or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e.to_string()))?;
    Ok((f, signed))
}

/// files_sha256: the sha256 of the canonical bytes of files.json.
pub fn files_sha256(files_json: &[u8]) -> Result<String, canonical::CanonicalError> {
    Ok(hex::encode(Sha256::digest(canonical::canonical(files_json)?)))
}

impl StoreKeys {
    fn verify_with(
        &self,
        signed: &[u8],
        sigs: &[Sig],
        kind: &str,
        use_: &str,
        step: &'static str,
        at: i64,
    ) -> Result<(), Error> {
        let mut last = Error { code: CODE_SIGNATURE_INVALID, step, msg: "no signature".into() };
        for s in sigs {
            let Some(k) = self.keys.get(&s.key_id) else {
                last = Error { code: CODE_SIGNATURE_INVALID, step, msg: format!("key {} is not listed", s.key_id) };
                continue;
            };
            if k.use_ != use_ {
                last = Error {
                    code: CODE_KEY_USE_MISMATCH,
                    step,
                    msg: format!("key {} is a {} key, not a {use_} key", s.key_id, k.use_),
                };
                continue;
            }
            if at < k.not_before || at > k.not_after {
                last = Error { code: CODE_SIGNATURE_INVALID, step, msg: format!("key {} is not valid then", s.key_id) };
                continue;
            }
            if verify_sig(&k.key, kind, signed, &s.sig) {
                return Ok(());
            }
            last = Error {
                code: CODE_SIGNATURE_INVALID,
                step,
                msg: format!("the signature by {} does not verify", s.key_id),
            };
        }
        Err(last)
    }

    /// The chain of §3 from store.sig on.
    pub fn verify_package(&self, p: &Package) -> Result<Verified, Error> {
        verify_package(p, Some(self))
    }

    fn list(&self, raw: &[u8], kind: &str) -> Result<(Env, i64), Error> {
        let env = envelope(raw, "list")?;
        let v = canonical::parse(&env.signed).or_else(|e| fail(CODE_SIGNATURE_INVALID, "list", e.to_string()))?;
        let issued = match &v {
            canonical::Value::Obj(m) => match m.get("issued") {
                Some(canonical::Value::Str(s)) => time_of("issued", s, "list")?,
                _ => return fail(CODE_SIGNATURE_INVALID, "list", "no issued time"),
            },
            _ => return fail(CODE_SIGNATURE_INVALID, "list", "signed is not an object"),
        };
        self.verify_with(&env.signed, &env.sigs, kind, "store", "list", issued)?;
        Ok((env, issued))
    }

    /// A newly received revoked.json (§4); answers its version.
    pub fn verify_revoked(&self, raw: &[u8], st: &State, clock: i64) -> Result<i64, Error> {
        let (env, issued) = self.list(raw, "revoked")?;
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Entry {
            plugin_id: String,
            versions: String,
            #[serde(default)]
            #[allow(dead_code)]
            developer_key_id: Option<String>,
            action: String,
            #[allow(dead_code)]
            reason_code: String,
            #[allow(dead_code)]
            issued: String,
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Signed {
            version: i64,
            #[allow(dead_code)]
            issued: String,
            expires: String,
            entries: Vec<Entry>,
        }
        let s: Signed = strict(&env.signed, "list")?;
        let expires = time_of("expires", &s.expires, "list")?;
        if expires <= issued || expires - issued > 48 * 3600 {
            return fail(CODE_SIGNATURE_INVALID, "list", "revoked.json claims more than 48 hours");
        }
        for e in &s.entries {
            if !matches!(e.action.as_str(), "disable" | "warn" | "delist")
                || e.plugin_id.is_empty()
                || e.versions.is_empty()
            {
                return fail(CODE_SIGNATURE_INVALID, "list", format!("entry {}: action {}", e.plugin_id, e.action));
            }
        }
        let sha = hex::encode(Sha256::digest(raw));
        if s.version < st.revoked_version {
            return fail(
                CODE_REVOCATION_ROLLBACK,
                "list",
                format!("version {} is older than {}", s.version, st.revoked_version),
            );
        }
        if s.version == st.revoked_version && !st.revoked_sha256.is_empty() && st.revoked_sha256 != sha {
            return fail(
                CODE_REVOCATION_ROLLBACK,
                "list",
                format!("version {} differs from the copy seen before", s.version),
            );
        }
        if st.now(clock) > expires {
            return fail(CODE_REVOCATION_EXPIRED, "list", format!("version {} expired", s.version));
        }
        Ok(s.version)
    }

    /// A revoked-head.json: its version and the sha256 it names.
    pub fn verify_revoked_head(&self, raw: &[u8]) -> Result<(i64, String), Error> {
        let (env, _) = self.list(raw, "revoked-head")?;
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Signed {
            version: i64,
            #[allow(dead_code)]
            issued: String,
            sha256: String,
        }
        let s: Signed = strict(&env.signed, "list")?;
        Ok((s.version, s.sha256))
    }

    /// A catalog for the plugin API major api_major; answers its version.
    pub fn verify_catalog(&self, raw: &[u8], api_major: i64, st: &State, clock: i64) -> Result<i64, Error> {
        let (env, issued) = self.list(raw, "catalog")?;
        #[derive(Deserialize)]
        struct Signed {
            version: i64,
            expires: String,
            api_major: i64,
        }
        let s: Signed = strict(&env.signed, "list")?;
        let expires = time_of("expires", &s.expires, "list")?;
        if s.api_major != api_major {
            return fail(CODE_SIGNATURE_INVALID, "list", "another plugin API major");
        }
        if expires <= issued || expires - issued > 7 * 86400 {
            return fail(CODE_SIGNATURE_INVALID, "list", "the catalog claims more than 7 days");
        }
        let seen = st.catalog_versions.get(&api_major.to_string()).copied().unwrap_or(0);
        if s.version < seen {
            return fail(CODE_CATALOG_ROLLBACK, "list", format!("version {} is older than {seen}", s.version));
        }
        if st.now(clock) > expires {
            return fail(CODE_CATALOG_EXPIRED, "list", format!("version {} expired", s.version));
        }
        Ok(s.version)
    }

    /// A signed entitlement (Phase 3): only a key of use entitlement may sign one.
    pub fn verify_entitlement(&self, raw: &[u8], st: &State, clock: i64) -> Result<(), Error> {
        let env = envelope(raw, "list")?;
        match self.verify_with(&env.signed, &env.sigs, "entitlement", "entitlement", "list", st.now(clock)) {
            Err(e) if e.code == CODE_KEY_USE_MISMATCH => Err(e),
            Err(e) => fail(CODE_ENTITLEMENT_INVALID, "list", e.msg),
            Ok(()) => Ok(()),
        }
    }
}

/// The package chain: store.sig (when keys and the file are there) → developer.sig →
/// every file. With no store-keys.json a store.sig is refused.
pub fn verify_package(p: &Package, keys: Option<&StoreKeys>) -> Result<Verified, Error> {
    let fsum =
        files_sha256(p.files_json).or_else(|e| fail(CODE_SIGNATURE_INVALID, "digest", format!("files.json: {e}")))?;
    let mut out = Verified { level: "none", developer_key_id: String::new(), files_sha256: fsum.clone() };
    let mut store_dev: Option<String> = None;
    if let Some(raw) = p.store_sig.filter(|b| !b.is_empty()) {
        let Some(keys) = keys else {
            return fail(CODE_SIGNATURE_INVALID, "store_keys", "no store-keys.json to check store.sig with");
        };
        let step = "store_sig";
        let (f, signed) = sig_file(raw, step)?;
        if f.public.is_some() {
            return fail(CODE_SIGNATURE_INVALID, step, "store.sig carries a public key");
        }
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Signed {
            plugin_id: String,
            version: String,
            files_sha256: String,
            developer_key_id: String,
            review_level: String,
            reviewed_at: String,
            capabilities_sha256: String,
        }
        let s: Signed = strict(&signed, step)?;
        let at = time_of("reviewed_at", &s.reviewed_at, step)?;
        keys.verify_with(&signed, &[Sig { key_id: f.key_id, sig: f.sig }], "store", "store", step, at)?;
        out.level = match s.review_level.as_str() {
            "automated" => "automated",
            "reviewed" => "reviewed",
            other => return fail(CODE_SIGNATURE_INVALID, step, format!("review level {other}")),
        };
        if s.files_sha256 != fsum {
            return fail(CODE_SIGNATURE_INVALID, step, "store.sig covers another files.json");
        }
        if s.plugin_id != p.plugin_id || s.version != p.version {
            return fail(CODE_SIGNATURE_INVALID, step, "store.sig is for another plugin or version");
        }
        if s.capabilities_sha256 != p.capabilities_sha256 {
            return fail(CODE_SIGNATURE_INVALID, step, "the capabilities differ from those the store reviewed");
        }
        store_dev = Some(s.developer_key_id);
    }
    match p.developer_sig.filter(|b| !b.is_empty()) {
        None => {
            if store_dev.is_some() {
                return fail(
                    CODE_SIGNATURE_INVALID,
                    "developer_sig",
                    "store.sig names a developer key and there is no developer.sig",
                );
            }
        }
        Some(raw) => {
            let step = "developer_sig";
            let (f, signed) = sig_file(raw, step)?;
            if let Some(want) = &store_dev {
                if &f.key_id != want {
                    return fail(
                        CODE_DEVELOPER_KEY_MISMATCH,
                        step,
                        format!("signed by {}, store.sig names {want}", f.key_id),
                    );
                }
            }
            let pk = decode_key(&f.key_id, f.public.as_deref().unwrap_or(""))
                .or_else(|e| fail(CODE_SIGNATURE_INVALID, step, e))?;
            if !verify_sig(&pk, "developer", &signed, &f.sig) {
                return fail(CODE_SIGNATURE_INVALID, step, format!("the signature by {} does not verify", f.key_id));
            }
            #[derive(Deserialize)]
            #[serde(deny_unknown_fields)]
            struct Signed {
                plugin_id: String,
                version: String,
                files_sha256: String,
            }
            let s: Signed = strict(&signed, step)?;
            if s.files_sha256 != fsum {
                return fail(CODE_SIGNATURE_INVALID, step, "developer.sig covers another files.json");
            }
            if s.plugin_id != p.plugin_id || s.version != p.version {
                return fail(CODE_SIGNATURE_INVALID, step, "developer.sig is for another plugin or version");
            }
            out.developer_key_id = f.key_id;
            if store_dev.is_none() {
                out.level = "developer";
            }
        }
    }
    if let Some(files) = p.files {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Line {
            path: String,
            sha256: String,
            size: u64,
        }
        let list: Vec<Line> = strict(p.files_json, "digest")?;
        for l in list {
            let ok = files
                .get(&l.path)
                .is_some_and(|b| b.len() as u64 == l.size && hex::encode(Sha256::digest(b)) == l.sha256);
            if !ok {
                return fail(CODE_SIGNATURE_INVALID, "digest", format!("{} does not match files.json", l.path));
            }
        }
    }
    Ok(out)
}

// ---- signing (the SDK's developer key: mkp keys new, mkp pack --key) --------------------

/// A developer's signing key.
pub struct Signer {
    key: SigningKey,
    pub id: String,
}

impl Signer {
    pub fn from_seed(seed: &[u8; 32]) -> Signer {
        let key = SigningKey::from_bytes(seed);
        let id = key_id(key.verifying_key().as_bytes());
        Signer { key, id }
    }

    pub fn public(&self) -> String {
        B64.encode(self.key.verifying_key().as_bytes())
    }

    pub fn sign(&self, kind: &str, signed: &[u8]) -> Result<String, canonical::CanonicalError> {
        Ok(B64.encode(self.key.sign(&message(kind, signed)?).to_bytes()))
    }

    /// developer.sig for files.json (54.14 §2), as canonical text.
    pub fn sign_developer(
        &self,
        plugin_id: &str,
        version: &str,
        files_json: &[u8],
    ) -> Result<String, canonical::CanonicalError> {
        let signed =
            serde_json::json!({"plugin_id": plugin_id, "version": version, "files_sha256": files_sha256(files_json)?});
        let c = canonical::canonical_of(&signed)?;
        let sig = self.sign("developer", &c)?;
        let file = serde_json::json!({"key_id": self.id, "public": self.public(), "sig": sig, "signed": signed});
        Ok(String::from_utf8(canonical::canonical_of(&file)?).expect("canonical JSON is UTF-8"))
    }
}
