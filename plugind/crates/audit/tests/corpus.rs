//! The store's component corpus (story 54.16 T01): tiny components made from WAT, committed in
//! store/checks/testdata/components/ so the store's checks run on real component bytes. This
//! test writes them when MAKTABA_WRITE_CORPUS is set and otherwise checks they are current,
//! and that the audit answers each one's expected code.
use maktaba_plugin_audit::{audit, component_names};

fn component(imports: &[&str], export_run: bool) -> String {
    let mut w = String::from("(component\n");
    for i in imports {
        w.push_str(&format!("  (import \"{i}\" (instance))\n"));
    }
    if export_run {
        w.push_str("  (core module $m (func (export \"run\")))\n  (core instance $i (instantiate $m))\n  (func (export \"run\") (canon lift (core func $i \"run\")))\n");
    }
    w.push(')');
    w
}

/// A case: its name, the imports, whether it exports run, the declared capabilities, the code.
type Case = (&'static str, &'static [&'static str], bool, &'static [&'static str], &'static str);

const CASES: &[Case] = &[
    (
        "good",
        &["maktaba:plugin/job@1.0.0", "maktaba:plugin/log@1.0.0", "maktaba:plugin/write@1.0.0"],
        true,
        &["tags.write"],
        "",
    ),
    (
        "forbidden-http",
        &["maktaba:plugin/job@1.0.0", "wasi:http/outgoing-handler@0.2.0"],
        true,
        &[],
        "plugin_forbidden_import",
    ),
    ("forbidden-sockets", &["wasi:sockets/tcp@0.2.0"], true, &[], "plugin_forbidden_import"),
    ("undeclared-media", &["maktaba:plugin/media@1.0.0"], true, &[], "plugin_undeclared_import"),
    ("no-run", &["maktaba:plugin/job@1.0.0"], false, &[], "plugin_missing_export"),
];

#[test]
fn the_corpus_is_current_and_audited() {
    let dir = std::path::Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../store/checks/testdata/components");
    let write = std::env::var_os("MAKTABA_WRITE_CORPUS").is_some();
    for (name, imports, run, declared, code) in CASES {
        let bytes = wat::parse_str(component(imports, *run)).unwrap();
        let path = dir.join(format!("{name}.wasm"));
        if write {
            std::fs::write(&path, &bytes).unwrap();
        } else {
            assert_eq!(
                std::fs::read(&path).unwrap_or_default(),
                bytes,
                "{name}: run with MAKTABA_WRITE_CORPUS=1 to write it"
            );
        }
        let (i, e) = component_names(&bytes).unwrap();
        let declared: Vec<String> = declared.iter().map(|s| s.to_string()).collect();
        let got = audit(&i, &e, &declared).err().map(|e| e.code).unwrap_or("");
        assert_eq!(got, *code, "{name}");
    }
}
