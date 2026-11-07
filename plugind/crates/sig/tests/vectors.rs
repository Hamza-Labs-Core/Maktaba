//! Story 54.14 T05, T07 (rust): the Rust verifier on the shared vectors the TypeScript
//! signer made; Go and TypeScript check the same file.

use base64::Engine;
use maktaba_plugin_sig::{canonical, parse_time, verify_package, Package, Root, Signer, State, StoreKeys};
use serde_json::Value;
use std::collections::BTreeMap;

fn dir() -> std::path::PathBuf {
    std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../shared/plugins/testdata/signatures")
}

fn vectors() -> Value {
    serde_json::from_slice(&std::fs::read(dir().join("vectors.json")).unwrap()).unwrap()
}

fn s<'a>(v: &'a Value, k: &str) -> &'a str {
    v[k].as_str().unwrap_or_else(|| panic!("{k}"))
}

fn now(v: &Value) -> i64 {
    parse_time(s(v, "now")).unwrap()
}

fn keys(v: &Value, name: &str) -> StoreKeys {
    let root = Root::parse(s(&v["roots"], "v1").as_bytes()).unwrap();
    root.verify_store_keys(s(&v["store_keys"], name).as_bytes(), &State::default(), now(v)).unwrap()
}

fn state(v: &Value) -> State {
    serde_json::from_value(v.clone()).unwrap()
}

fn check<T: std::fmt::Debug>(name: &str, got: Result<T, maktaba_plugin_sig::Error>, expect: &Value) -> Option<T> {
    if expect["ok"].as_bool().unwrap() {
        Some(got.unwrap_or_else(|e| panic!("{name}: want ok, got {e}")))
    } else {
        let e = got.err().unwrap_or_else(|| panic!("{name}: want a refusal"));
        assert_eq!((e.code, e.step), (s(expect, "code"), s(expect, "step")), "{name}: {e}");
        None
    }
}

#[test]
fn canonical_json() {
    let f: Value = serde_json::from_slice(&std::fs::read(dir().join("canonical.json")).unwrap()).unwrap();
    for c in f["cases"].as_array().unwrap() {
        let got = canonical::canonical(s(c, "input").as_bytes());
        if c["refused"].as_bool() == Some(true) {
            assert!(got.is_err(), "{}: want refused", s(c, "name"));
        } else {
            assert_eq!(String::from_utf8(got.unwrap()).unwrap(), s(c, "output"), "{}", s(c, "name"));
        }
    }
}

#[test]
fn packages() {
    let v = vectors();
    let b64 = base64::engine::general_purpose::STANDARD;
    for c in v["package_cases"].as_array().unwrap() {
        let name = s(c, "name");
        let p = &c["package"];
        let files: BTreeMap<String, Vec<u8>> = p["files"]
            .as_object()
            .unwrap()
            .iter()
            .map(|(k, x)| (k.clone(), b64.decode(x.as_str().unwrap()).unwrap()))
            .collect();
        let k = p["store_keys"].as_str().map(|n| keys(&v, n));
        let pkg = Package {
            files_json: s(p, "files_json").as_bytes(),
            developer_sig: p["developer_sig"].as_str().map(str::as_bytes),
            store_sig: p["store_sig"].as_str().map(str::as_bytes),
            plugin_id: s(p, "plugin_id"),
            version: s(p, "version"),
            capabilities_sha256: s(p, "capabilities_sha256"),
            files: Some(&files),
        };
        if let Some(r) = check(name, verify_package(&pkg, k.as_ref()), &c["expect"]) {
            assert_eq!(r.level, s(&c["expect"], "level"), "{name}");
        }
    }
}

#[test]
fn store_keys() {
    let v = vectors();
    let root = Root::parse(s(&v["roots"], "v1").as_bytes()).unwrap();
    for c in v["store_keys_cases"].as_array().unwrap() {
        let got = root.verify_store_keys(
            s(&v["store_keys"], s(c, "raw")).as_bytes(),
            &state(&c["state"]),
            parse_time(s(c, "now")).unwrap(),
        );
        if let Some(k) = check(s(c, "name"), got.map(|k| k.version), &c["expect"]) {
            assert_eq!(k, c["expect"]["version"].as_i64().unwrap());
        }
    }
    let empty = Root::parse(s(&v["roots"], "empty").as_bytes()).unwrap();
    let e = empty.verify_store_keys(s(&v["store_keys"], "v3").as_bytes(), &State::default(), now(&v)).err().unwrap();
    assert_eq!((e.code, e.step), ("plugin_signature_invalid", "root"));
}

#[test]
fn lists() {
    let v = vectors();
    let k = keys(&v, "v3");
    for c in v["revoked_cases"].as_array().unwrap() {
        let got = k.verify_revoked(
            s(&v["revoked"], s(c, "raw")).as_bytes(),
            &state(&c["state"]),
            parse_time(s(c, "now")).unwrap(),
        );
        if let Some(ver) = check(s(c, "name"), got, &c["expect"]) {
            assert_eq!(ver, c["expect"]["version"].as_i64().unwrap());
        }
    }
    for c in v["head_cases"].as_array().unwrap() {
        check(s(c, "name"), k.verify_revoked_head(s(&v["heads"], s(c, "raw")).as_bytes()), &c["expect"]);
    }
    for c in v["catalog_cases"].as_array().unwrap() {
        let got = k.verify_catalog(
            s(&v["catalogs"], s(c, "raw")).as_bytes(),
            c["api_major"].as_i64().unwrap(),
            &state(&c["state"]),
            parse_time(s(c, "now")).unwrap(),
        );
        check(s(c, "name"), got, &c["expect"]);
    }
    for c in v["entitlement_cases"].as_array().unwrap() {
        check(s(c, "name"), k.verify_entitlement(s(c, "raw").as_bytes(), &State::default(), now(&v)), &c["expect"]);
    }
}

#[test]
fn root_chaining() {
    let v = vectors();
    for c in v["root_cases"].as_array().unwrap() {
        let old = Root::parse(s(&v["roots"], s(c, "old")).as_bytes()).unwrap();
        check(
            s(c, "name"),
            old.verify_update(s(&v["roots"], s(c, "next")).as_bytes()).map(|r| r.version),
            &c["expect"],
        );
    }
}

/// The SDK signs what the TypeScript signer signs: the same seed gives the same key id and
/// a developer.sig the vectors' "developer only" case accepts.
#[test]
fn the_sdk_signs_the_same() {
    use sha2::Digest;
    let v = vectors();
    let seed: [u8; 32] = sha2::Sha256::digest(b"maktaba-plugins TEST KEY, never for production: developer-1").into();
    let signer = Signer::from_seed(&seed);
    assert_eq!(signer.id, s(&v["keys"]["dev1"], "id"));
    let c = &v["package_cases"][0]["package"];
    let sig = signer.sign_developer(s(c, "plugin_id"), s(c, "version"), s(c, "files_json").as_bytes()).unwrap();
    assert_eq!(sig, s(c, "developer_sig"));
}
