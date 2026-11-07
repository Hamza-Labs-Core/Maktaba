//! mkp end to end (story 54.12 T01, T03): a project from the template validates, runs in the
//! harness and packs into the home's format (manifest.toml, then files.json, then the files).
//! The guest is the host's hello (`make plugind-guests` first).

use std::io::Read;
use std::path::Path;
use std::process::Command;

fn mkp(args: &[&str]) -> std::process::Output {
    Command::new(env!("CARGO_BIN_EXE_mkp")).args(args).output().unwrap()
}

fn hello() -> Vec<u8> {
    let p = Path::new(env!("CARGO_MANIFEST_DIR")).join("../../guests/target/wasm32-wasip2/release/hello.wasm");
    std::fs::read(&p).unwrap_or_else(|_| panic!("{} is missing: run `make plugind-guests` first", p.display()))
}

#[test]
fn new_check_test_and_pack() {
    let dir = tempfile::tempdir().unwrap();
    let proj = dir.path().join("greeter");
    let out = mkp(&["new", "rust", "dev.example.greeter", proj.to_str().unwrap()]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    assert!(proj.join("src/lib.rs").exists() && proj.join("wit/maktaba-plugin.wit").exists());
    let pkg = proj.join("package");
    std::fs::write(pkg.join("plugin.wasm"), hello()).unwrap();

    let out = mkp(&["check", pkg.to_str().unwrap()]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));

    let fixtures = dir.path().join("api.json");
    std::fs::write(&fixtures, r#"{"library/video": {"id": "v1"}}"#).unwrap();
    let out = mkp(&["test", pkg.to_str().unwrap(), "--stage", "hello", "--fixtures", fixtures.to_str().unwrap()]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    let report: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    assert_eq!(report["result"]["outcome"], "ok");
    assert_eq!(report["logs"][0]["service"], "plugin.dev.example.greeter");

    let file = dir.path().join("g.mkplugin");
    let out = mkp(&["pack", pkg.to_str().unwrap(), file.to_str().unwrap()]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    let dec = zstd::stream::read::Decoder::new(std::fs::File::open(&file).unwrap()).unwrap();
    let mut tar = tar::Archive::new(dec);
    let mut names = Vec::new();
    let mut files_json = String::new();
    for e in tar.entries().unwrap() {
        let mut e = e.unwrap();
        let name = e.path().unwrap().to_string_lossy().into_owned();
        if name == "files.json" {
            e.read_to_string(&mut files_json).unwrap();
        }
        names.push(name);
    }
    assert_eq!(&names[..2], ["manifest.toml", "files.json"]);
    let listed: Vec<serde_json::Value> = serde_json::from_str(&files_json).unwrap();
    assert_eq!(listed.len(), 4, "{files_json}");
    assert!(files_json.starts_with(r#"[{"path":"i18n/ar.json","sha256":""#), "the Go writer's form: {files_json}");
}

#[test]
fn a_bad_manifest_and_a_missing_stage_are_refused() {
    let dir = tempfile::tempdir().unwrap();
    std::fs::write(dir.path().join("manifest.toml"), "schema = 1\nid = \"x\"\n").unwrap();
    let out = mkp(&["check", dir.path().to_str().unwrap()]);
    assert!(!out.status.success());
    assert!(String::from_utf8_lossy(&out.stderr).contains("manifest_"), "{}", String::from_utf8_lossy(&out.stderr));
    let out = mkp(&["new", "rust", "not-an-id", dir.path().join("p").to_str().unwrap()]);
    assert!(!out.status.success());
}

/// Story 54.14 §1, §2: `mkp keys new` makes a key whose private half is the owner's only,
/// `export-public` prints the public half, and `mkp pack --key` signs files.json: the
/// home's verifier (here its Rust twin) accepts it at level developer, and a byte changed
/// in a file after packing fails at the digest step.
#[test]
fn keys_and_a_signed_pack() {
    let dir = tempfile::tempdir().unwrap();
    let keys = dir.path().join("keys");
    let run =
        |args: &[&str]| Command::new(env!("CARGO_BIN_EXE_mkp")).args(args).env("MKP_KEYS_DIR", &keys).output().unwrap();
    let out = run(&["keys", "new"]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));
    let id = String::from_utf8_lossy(&out.stdout).split_whitespace().next().unwrap().to_string();
    assert_eq!(id.len(), 32);
    let seed = std::fs::read_to_string(keys.join(format!("{id}.key"))).unwrap();
    assert!(!String::from_utf8_lossy(&out.stdout).contains(seed.trim()), "the private key was printed");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = std::fs::metadata(keys.join(format!("{id}.key"))).unwrap().permissions().mode();
        assert_eq!(mode & 0o077, 0, "the key file is readable by others: {mode:o}");
    }
    let out = run(&["keys", "export-public"]);
    let public: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    assert_eq!(public["id"], id.as_str());
    assert!(!String::from_utf8_lossy(&out.stdout).contains(seed.trim()));

    let proj = dir.path().join("p");
    assert!(run(&["new", "rust", "dev.example.signed", proj.to_str().unwrap()]).status.success());
    let pkg = proj.join("package");
    std::fs::write(pkg.join("plugin.wasm"), b"\0asm-not-really").unwrap();
    let file = dir.path().join("s.mkplugin");
    let out = run(&["pack", pkg.to_str().unwrap(), file.to_str().unwrap(), "--key", &id]);
    assert!(out.status.success(), "{}", String::from_utf8_lossy(&out.stderr));

    let dec = zstd::stream::read::Decoder::new(std::fs::File::open(&file).unwrap()).unwrap();
    let mut tar = tar::Archive::new(dec);
    let mut entries: std::collections::BTreeMap<String, Vec<u8>> = Default::default();
    let mut order = Vec::new();
    for e in tar.entries().unwrap() {
        let mut e = e.unwrap();
        let name = e.path().unwrap().to_string_lossy().into_owned();
        let mut b = Vec::new();
        e.read_to_end(&mut b).unwrap();
        order.push(name.clone());
        entries.insert(name, b);
    }
    assert_eq!(&order[..3], ["manifest.toml", "files.json", "signatures/developer.sig"]);
    let fj = entries.remove("files.json").unwrap();
    let dev = entries.remove("signatures/developer.sig").unwrap();
    let pkgv = |files: &std::collections::BTreeMap<String, Vec<u8>>| {
        maktaba_plugin_sig::verify_package(
            &maktaba_plugin_sig::Package {
                files_json: &fj,
                developer_sig: Some(&dev),
                store_sig: None,
                plugin_id: "dev.example.signed",
                version: "0.1.0",
                capabilities_sha256: "",
                files: Some(files),
            },
            None,
        )
    };
    let v = pkgv(&entries).unwrap();
    assert_eq!((v.level, v.developer_key_id.as_str()), ("developer", id.as_str()));
    entries.get_mut("plugin.wasm").unwrap()[0] ^= 1;
    let e = pkgv(&entries).unwrap_err();
    assert_eq!((e.code, e.step), ("plugin_signature_invalid", "digest"));
}
