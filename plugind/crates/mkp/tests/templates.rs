//! The Python and JavaScript templates end to end (story 54.12 T01, T05): `mkp new`,
//! `mkp build` (the toolchain, the narrowed world, the home's checks), `mkp test` in the
//! harness, and the Python package's size. They need componentize-py and jco: run with
//! MKP_TEST_TOOLCHAINS=1 (CI's SDK templates step installs both; MKP_COMPONENTIZE_PY and
//! MKP_JCO may name them).

use std::path::Path;
use std::process::{Command, Output};

fn on() -> bool {
    if std::env::var("MKP_TEST_TOOLCHAINS").as_deref() == Ok("1") {
        return true;
    }
    eprintln!("skipped: set MKP_TEST_TOOLCHAINS=1 with componentize-py and jco installed");
    false
}

fn mkp(args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_mkp")).args(args).output().unwrap()
}

fn ok(out: &Output, what: &str) {
    assert!(
        out.status.success(),
        "{what}: {}\n{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
}

/// new, build and test: the hello stage answers with the subject's hash and logs a line.
fn hello(lang: &str, dir: &Path) {
    let p = dir.to_str().unwrap();
    ok(&mkp(&["new", lang, &format!("dev.example.hello{lang}"), p]), "mkp new");
    ok(&mkp(&["build", p]), "mkp build");
    let out = mkp(&["test", &format!("{p}/package"), "--stage", "hello"]);
    ok(&out, "mkp test");
    let v: serde_json::Value = serde_json::from_slice(&out.stdout).unwrap();
    assert_eq!(v["result"]["outcome"], "ok", "{v}");
    assert_eq!(v["result"]["output_hash"], "h-fixture", "{v}");
    assert_eq!(v["logs"].as_array().map(Vec::len), Some(1), "{v}");
}

#[test]
fn python_hello_builds_runs_and_packs_small() {
    if !on() {
        return;
    }
    let t = tempfile::tempdir().unwrap();
    let dir = t.path().join("hello-py");
    hello("python", &dir);
    // T05 (N15): the Python hello packed is at most 25 MB
    let pkg = t.path().join("hello.mkplugin");
    ok(&mkp(&["pack", dir.join("package").to_str().unwrap(), pkg.to_str().unwrap()]), "mkp pack");
    let size = std::fs::metadata(&pkg).unwrap().len();
    assert!(size <= 25 << 20, "the Python hello packs to {size} bytes, over 25 MB");
}

#[test]
fn js_hello_builds_and_runs() {
    if !on() {
        return;
    }
    let t = tempfile::tempdir().unwrap();
    hello("js", &t.path().join("hello-js"));
}

/// A plugin that calls an interface its manifest does not declare does not build (the world
/// it is built against has no such import); declaring the capability that covers it, the
/// same code builds.
#[test]
fn an_undeclared_interface_does_not_build() {
    if !on() {
        return;
    }
    let t = tempfile::tempdir().unwrap();
    let dir = t.path().join("tags");
    let p = dir.to_str().unwrap();
    ok(&mkp(&["new", "js", "dev.example.tags", p]), "mkp new");
    let src = std::fs::read_to_string(dir.join("plugin.js")).unwrap();
    let src = src.replace(
        "import { write } from \"maktaba:plugin/log@1.2.0\";",
        "import { write } from \"maktaba:plugin/log@1.2.0\";\nimport { addTags } from \"maktaba:plugin/write@1.2.0\";\nexport const tag = addTags;",
    );
    std::fs::write(dir.join("plugin.js"), src).unwrap();
    let out = mkp(&["build", p]);
    assert!(!out.status.success(), "a plugin importing write without tags.write built");

    let manifest = dir.join("package/manifest.toml");
    let m = std::fs::read_to_string(&manifest).unwrap();
    std::fs::write(&manifest, format!("{m}\n[capabilities]\nrequired = [\"tags.write\"]\n")).unwrap();
    ok(&mkp(&["build", p]), "mkp build with tags.write declared");
    let wit = std::fs::read_to_string(dir.join("wit/maktaba-plugin.wit")).unwrap();
    assert!(wit.contains("  import write;"), "the world has no write import");
}
