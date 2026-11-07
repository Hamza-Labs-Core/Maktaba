//! A `.mkplugin` read and written in memory, by the home's rules (the Go reader,
//! `componentpack.UnpackPlugin`, and writer, `BuildPlugin`; story 54.01 §1, 54.38 §3).

use serde_json::{json, Value};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet};
use std::io::Read;
use unicode_normalization::is_nfc;

pub const MAX_PACKED: usize = 50 << 20;
pub const MAX_UNPACKED: u64 = 200 << 20;
pub const MAX_FILES: usize = 500;
pub const MAX_PATH: usize = 200;
const MAX_HEAD: u64 = 1 << 20;
const MAX_SIG: u64 = 16 << 10;
pub const DEVELOPER_SIG: &str = "signatures/developer.sig";
pub const STORE_SIG: &str = "signatures/store.sig";

/// A refusal with the home's code (componentpack's package_* or a manifest_* code).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct Refusal {
    pub code: String,
    pub detail: String,
}

fn no<T>(code: &str, detail: impl Into<String>) -> Result<T, Refusal> {
    Err(Refusal { code: code.into(), detail: detail.into() })
}

/// One tar entry: its path, type flag and body.
#[derive(Debug, Clone)]
pub struct Entry {
    pub name: String,
    pub kind: u8,
    pub body: Vec<u8>,
}

fn octal(field: &[u8]) -> Result<u64, Refusal> {
    let s: String = field.iter().take_while(|&&c| c != 0).map(|&c| c as char).collect();
    let s = s.trim();
    if s.is_empty() {
        return Ok(0);
    }
    u64::from_str_radix(s, 8).or_else(|_| no("package_bad_head", "a bad size in a tar header"))
}

fn cstr(field: &[u8]) -> String {
    String::from_utf8_lossy(&field[..field.iter().position(|&c| c == 0).unwrap_or(field.len())]).into_owned()
}

/// The entries of a tar (ustar, with PAX path and size records), in order.
pub fn read_tar(data: &[u8]) -> Result<Vec<Entry>, Refusal> {
    let mut out = Vec::new();
    let mut i = 0usize;
    let mut pax: BTreeMap<String, String> = BTreeMap::new();
    while i + 512 <= data.len() {
        let h = &data[i..i + 512];
        if h.iter().all(|&c| c == 0) {
            return Ok(out);
        }
        let sum: u64 = h.iter().enumerate().map(|(j, &c)| if (148..156).contains(&j) { 32 } else { c as u64 }).sum();
        if octal(&h[148..156])? != sum {
            return no("package_bad_head", "a tar header's checksum is wrong");
        }
        let kind = if h[156] == 0 { b'0' } else { h[156] };
        let mut size = octal(&h[124..136])?;
        let mut name = cstr(&h[0..100]);
        if &h[257..262] == b"ustar" {
            let prefix = cstr(&h[345..500]);
            if !prefix.is_empty() {
                name = format!("{prefix}/{name}");
            }
        }
        if let Some(p) = pax.remove("path") {
            name = p;
        }
        if let Some(s) = pax.remove("size") {
            size = s.parse().or_else(|_| no("package_bad_head", "a bad PAX size"))?;
        }
        pax.clear();
        i += 512;
        let n = usize::try_from(size).or_else(|_| no("package_too_large", "an entry is too large"))?;
        if i + n > data.len() {
            return no("package_bad_head", "the tar ends inside an entry");
        }
        let body = data[i..i + n].to_vec();
        i += n.div_ceil(512) * 512;
        match kind {
            b'x' => {
                let text = String::from_utf8_lossy(&body).into_owned();
                let mut rest = text.as_str();
                while !rest.is_empty() {
                    let (len, _) = rest
                        .split_once(' ')
                        .ok_or(Refusal { code: "package_bad_head".into(), detail: "a bad PAX record".into() })?;
                    let len: usize = len.parse().or_else(|_| no("package_bad_head", "a bad PAX record"))?;
                    if len == 0 || len > rest.len() {
                        return no("package_bad_head", "a bad PAX record");
                    }
                    let rec = &rest[..len];
                    if let Some((_, kv)) = rec.trim_end_matches('\n').split_once(' ') {
                        if let Some((k, v)) = kv.split_once('=') {
                            pax.insert(k.to_string(), v.to_string());
                        }
                    }
                    rest = &rest[len..];
                }
            }
            b'g' => {}
            _ => out.push(Entry { name, kind, body }),
        }
    }
    Ok(out)
}

fn header(name: &str, size: usize, kind: u8) -> [u8; 512] {
    let mut h = [0u8; 512];
    let put = |h: &mut [u8; 512], at: usize, s: &[u8]| h[at..at + s.len()].copy_from_slice(s);
    put(&mut h, 0, &name.as_bytes()[..name.len().min(100)]);
    put(&mut h, 100, b"0000644\0");
    put(&mut h, 108, b"0000000\0");
    put(&mut h, 116, b"0000000\0");
    put(&mut h, 124, format!("{size:011o}\0").as_bytes());
    put(&mut h, 136, b"00000000000\0");
    h[156] = kind;
    put(&mut h, 257, b"ustar\x0000");
    put(&mut h, 148, b"        ");
    let sum: u32 = h.iter().map(|&c| c as u32).sum();
    put(&mut h, 148, format!("{sum:06o}\0 ").as_bytes());
    h
}

fn pad(out: &mut Vec<u8>) {
    let r = out.len() % 512;
    if r != 0 {
        out.resize(out.len() + 512 - r, 0);
    }
}

/// A tar of entries: ustar headers, a PAX record for a path over 100 bytes.
pub fn write_tar(entries: &[Entry]) -> Vec<u8> {
    let mut out = Vec::new();
    for e in entries {
        if e.name.len() > 100 {
            let mut rec = format!(" path={}\n", e.name);
            let mut len = rec.len();
            loop {
                let total = len.to_string().len() + rec.len();
                if total == len {
                    break;
                }
                len = total;
            }
            rec = format!("{len}{rec}");
            out.extend_from_slice(&header("PaxHeader", rec.len(), b'x'));
            out.extend_from_slice(rec.as_bytes());
            pad(&mut out);
        }
        out.extend_from_slice(&header(&e.name, e.body.len(), e.kind));
        out.extend_from_slice(&e.body);
        pad(&mut out);
    }
    out.extend_from_slice(&[0u8; 1024]);
    out
}

/// The home's SafePath with the plugin limits (componentpack.SafePluginPath).
pub fn safe_plugin_path(p: &str) -> bool {
    if p.len() > MAX_PATH || !is_nfc(p) || p == "files.json" || p == DEVELOPER_SIG || p == STORE_SIG {
        return false;
    }
    if p.is_empty() || p.starts_with('/') || p.ends_with('/') {
        return false;
    }
    if p.chars().any(|c| (c as u32) < 0x20 || c == '\u{7f}' || c == '\\' || c == ':') {
        return false;
    }
    p.split('/').all(|el| !el.is_empty() && el != "." && el != "..")
}

fn decompress(packed: &[u8]) -> Result<Vec<u8>, Refusal> {
    if packed.len() > MAX_PACKED {
        return no("package_too_large", format!("the package is over {MAX_PACKED} bytes"));
    }
    let mut d = ruzstd::decoding::StreamingDecoder::new(packed)
        .or_else(|e| no("package_bad_head", format!("not zstd: {e}")))?;
    let mut out = Vec::new();
    // the tar of at most the unpacked limit, its headers and a margin
    let limit = MAX_UNPACKED + (MAX_FILES as u64 + 8) * 2048 + 4 * MAX_HEAD;
    (&mut d).take(limit + 1).read_to_end(&mut out).or_else(|e| no("package_bad_head", format!("not zstd: {e}")))?;
    if out.len() as u64 > limit {
        return no("package_too_large", "the package unpacks to too much");
    }
    Ok(out)
}

fn sha_hex(b: &[u8]) -> String {
    hex::encode(Sha256::digest(b))
}

/// A package as read.
pub struct Inspected {
    /// The component's import and export names and the host's import audit (54.16 C4):
    /// None when it passed, else the refusal.
    pub imports: Vec<String>,
    pub exports: Vec<String>,
    pub audit: Option<Refusal>,
    /// The package's i18n words by language (54.16 C6).
    pub strings: BTreeMap<String, Value>,
    pub wasm_size: u64,
    pub manifest: serde_json::Map<String, Value>,
    pub manifest_text: Vec<u8>,
    pub files_json: Vec<u8>,
    pub developer_sig: Option<Vec<u8>>,
    pub store_sig: Option<Vec<u8>>,
    pub files: Vec<(String, String, u64)>,
    pub entries: Vec<Entry>,
}

impl Inspected {
    pub fn to_json(&self) -> String {
        let text = |b: &Option<Vec<u8>>| b.as_ref().map(|b| String::from_utf8_lossy(b).into_owned());
        json!({
            "manifest": maktaba_plugin_manifest::normalised(&self.manifest),
            "files_json": String::from_utf8_lossy(&self.files_json),
            "developer_sig": text(&self.developer_sig),
            "store_sig": text(&self.store_sig),
            "files": self.files.iter().map(|(p, s, n)| json!({"path": p, "sha256": s, "size": n})).collect::<Vec<_>>(),
            "unpacked_bytes": self.files.iter().map(|f| f.2).sum::<u64>(),
            "imports": self.imports,
            "exports": self.exports,
            "audit": self.audit.as_ref().map(|r| json!({"code": r.code, "detail": r.detail})),
            "strings": self.strings,
            "wasm_size": self.wasm_size,
        })
        .to_string()
    }
}

/// Reads and checks a package in memory, by the home's rules.
pub fn inspect(packed: &[u8]) -> Result<Inspected, Refusal> {
    let entries = read_tar(&decompress(packed)?)?;
    let mut it = entries.iter();
    let mut head = |want: &str| -> Result<Vec<u8>, Refusal> {
        match it.next() {
            Some(e) if e.name == want && e.kind == b'0' => {
                if e.body.len() as u64 > MAX_HEAD {
                    return no("package_too_large", format!("{want} is over {MAX_HEAD} bytes"));
                }
                Ok(e.body.clone())
            }
            Some(e) => no("package_bad_head", format!("the entry {:?} comes where {want} must", e.name)),
            None => no("package_bad_head", format!("no {want}")),
        }
    };
    let manifest_text = head("manifest.toml")?;
    let files_json = head("files.json")?;
    let list: Vec<Value> =
        serde_json::from_slice(&files_json).or_else(|e| no("package_bad_head", format!("files.json: {e}")))?;
    if list.len() > MAX_FILES {
        return no("package_too_many_files", format!("{} files, at most {MAX_FILES}", list.len()));
    }
    let mut listed: BTreeMap<String, (String, u64)> = BTreeMap::new();
    let mut prev: Option<String> = None;
    let mut total = 0u64;
    let hex_ok = |s: &str| s.len() == 64 && s.bytes().all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c));
    for v in &list {
        let o = v
            .as_object()
            .ok_or(Refusal { code: "package_bad_head".into(), detail: "files.json: a line is not an object".into() })?;
        if o.keys().any(|k| k != "path" && k != "sha256" && k != "size") {
            return no("package_bad_head", "files.json: an unknown field");
        }
        let (Some(p), Some(s), Some(n)) = (
            o.get("path").and_then(Value::as_str),
            o.get("sha256").and_then(Value::as_str),
            o.get("size").and_then(Value::as_u64),
        ) else {
            return no("package_bad_head", "files.json: a line lacks path, sha256 or size");
        };
        if !safe_plugin_path(p) {
            return no("package_bad_path", format!("bad path {p:?}"));
        }
        if prev.as_deref().is_some_and(|q| q >= p) {
            return no("package_bad_head", format!("files.json is not sorted by path, or lists {p:?} twice"));
        }
        if !hex_ok(s) {
            return no("package_bad_head", format!("{p}: bad sha256"));
        }
        total += n;
        prev = Some(p.to_string());
        listed.insert(p.to_string(), (s.to_string(), n));
    }
    if total > MAX_UNPACKED {
        return no("package_too_large", format!("{total} bytes unpacked, at most {MAX_UNPACKED}"));
    }
    match listed.get("manifest.toml") {
        None => return no("package_missing_file", "files.json does not list manifest.toml"),
        Some((s, n)) if *s != sha_hex(&manifest_text) || *n != manifest_text.len() as u64 => {
            return no("package_digest_mismatch", "manifest.toml does not match files.json")
        }
        _ => {}
    }
    let mut bodies: BTreeMap<String, Vec<u8>> = BTreeMap::new();
    bodies.insert("manifest.toml".into(), manifest_text.clone());
    let (mut developer_sig, mut store_sig) = (None, None);
    let mut done = BTreeSet::from(["manifest.toml".to_string()]);
    for e in it {
        if e.kind == b'5' {
            let p = e.name.trim_end_matches('/');
            if !p.split('/').all(|el| !el.is_empty() && el != "." && el != "..") || p.starts_with('/') {
                return no("package_bad_path", format!("bad folder path {:?}", e.name));
            }
            continue;
        }
        if e.name == DEVELOPER_SIG || e.name == STORE_SIG {
            if e.kind != b'0' || e.body.len() as u64 > MAX_SIG {
                return no("package_bad_entry", format!("{} is not a small regular file", e.name));
            }
            if e.name == DEVELOPER_SIG {
                developer_sig = Some(e.body.clone());
            } else {
                store_sig = Some(e.body.clone());
            }
            continue;
        }
        if !safe_plugin_path(&e.name) {
            return no("package_bad_path", format!("bad path {:?}", e.name));
        }
        if e.kind != b'0' {
            return no(
                "package_bad_entry",
                format!("{}: entry type {:?} is not a regular file", e.name, e.kind as char),
            );
        }
        let Some((s, n)) = listed.get(&e.name) else {
            return no("package_unlisted_file", format!("{} is not listed in files.json", e.name));
        };
        if !done.insert(e.name.clone()) {
            return no("package_bad_entry", format!("{} comes twice", e.name));
        }
        if e.body.len() as u64 != *n || sha_hex(&e.body) != *s {
            return no("package_digest_mismatch", format!("{} does not match files.json", e.name));
        }
        bodies.insert(e.name.clone(), e.body.clone());
    }
    for p in listed.keys() {
        if !done.contains(p) {
            return no("package_missing_file", format!("{p} is listed in files.json but not in the package"));
        }
    }
    let lookup = |p: &str| -> Option<Vec<u8>> { bodies.get(p).cloned() };
    let manifest = maktaba_plugin_manifest::validate(&manifest_text, Some(&lookup)).map_err(|errs| Refusal {
        code: errs[0].code.clone(),
        detail: errs.iter().map(|e| e.to_string()).collect::<Vec<_>>().join("; "),
    })?;
    let files = listed.into_iter().map(|(p, (s, n))| (p, s, n)).collect();
    // C4: the host's own audit on the component's names
    let wasm = bodies.get("plugin.wasm").cloned().unwrap_or_default();
    let declared: Vec<String> = ["required", "optional"]
        .iter()
        .flat_map(|k| manifest["capabilities"][*k].as_array().cloned().unwrap_or_default())
        .filter_map(|v| v.as_str().map(str::to_string))
        .collect();
    let (imports, exports, audit) = match maktaba_plugin_audit::component_names(&wasm) {
        Ok((i, e)) => {
            let a = maktaba_plugin_audit::audit(&i, &e, &declared)
                .err()
                .map(|e| Refusal { code: e.code.into(), detail: e.detail });
            (i, e, a)
        }
        Err(e) => (vec![], vec![], Some(Refusal { code: e.code.into(), detail: e.detail })),
    };
    let mut strings = BTreeMap::new();
    for lang in ["en", "ar"] {
        if let Some(b) = bodies.get(&format!("i18n/{lang}.json")) {
            strings.insert(lang.to_string(), serde_json::from_slice(b).unwrap_or(Value::Null));
        }
    }
    let wasm_size = wasm.len() as u64;
    Ok(Inspected {
        imports,
        exports,
        audit,
        strings,
        wasm_size,
        manifest,
        manifest_text,
        files_json,
        developer_sig,
        store_sig,
        files,
        entries,
    })
}

/// The package with signatures/store.sig set: manifest.toml, files.json, developer.sig, the
/// new store.sig, then every other entry in its order, compressed again.
pub fn repack(packed: &[u8], store_sig: &[u8]) -> Result<Vec<u8>, Refusal> {
    let i = inspect(packed)?;
    if store_sig.len() as u64 > MAX_SIG {
        return no("package_bad_entry", "store.sig is too large");
    }
    let mut out = Vec::with_capacity(i.entries.len() + 1);
    let mut rest = Vec::new();
    for e in i.entries {
        match e.name.as_str() {
            "manifest.toml" | "files.json" | DEVELOPER_SIG => out.push(e),
            STORE_SIG => {}
            _ => rest.push(e),
        }
    }
    out.push(Entry { name: STORE_SIG.into(), kind: b'0', body: store_sig.to_vec() });
    out.extend(rest);
    let tar = write_tar(&out);
    let packed = ruzstd::encoding::compress_to_vec(tar.as_slice(), ruzstd::encoding::CompressionLevel::Fastest);
    if packed.len() > MAX_PACKED {
        return no("package_too_large", "the countersigned package is over the packed limit");
    }
    Ok(packed)
}

/// A package from files (path → bytes) and signatures (entry name → bytes), in the home's
/// writer's form: manifest.toml, files.json (canonical), the signatures, the files by path.
/// The store's tests build their packages with it.
pub fn pack(files: &BTreeMap<String, Vec<u8>>, sigs: &BTreeMap<String, Vec<u8>>) -> Result<Vec<u8>, Refusal> {
    let manifest = files
        .get("manifest.toml")
        .ok_or(Refusal { code: "package_missing_file".into(), detail: "no manifest.toml".into() })?;
    let lines: Vec<String> = files
        .iter()
        .map(|(p, b)| {
            format!(
                r#"{{"path":{},"sha256":"{}","size":{}}}"#,
                serde_json::to_string(p).expect("a path"),
                sha_hex(b),
                b.len()
            )
        })
        .collect();
    let fj = format!("[{}]", lines.join(","));
    let mut entries = vec![
        Entry { name: "manifest.toml".into(), kind: b'0', body: manifest.clone() },
        Entry { name: "files.json".into(), kind: b'0', body: fj.into_bytes() },
    ];
    for name in [DEVELOPER_SIG, STORE_SIG] {
        if let Some(b) = sigs.get(name) {
            entries.push(Entry { name: name.into(), kind: b'0', body: b.clone() });
        }
    }
    for (p, b) in files {
        if p != "manifest.toml" {
            entries.push(Entry { name: p.clone(), kind: b'0', body: b.clone() });
        }
    }
    Ok(ruzstd::encoding::compress_to_vec(write_tar(&entries).as_slice(), ruzstd::encoding::CompressionLevel::Fastest))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn pkg(files: &[(&str, &[u8])], sigs: &[(&str, &[u8])]) -> Vec<u8> {
        let mut list: Vec<_> =
            files.iter().map(|(p, b)| json!({"path": p, "sha256": sha_hex(b), "size": b.len()})).collect();
        list.sort_by(|a, b| a["path"].as_str().cmp(&b["path"].as_str()));
        let fj = serde_json::to_vec(&list).unwrap();
        let mut entries = vec![
            Entry {
                name: "manifest.toml".into(),
                kind: b'0',
                body: files.iter().find(|f| f.0 == "manifest.toml").unwrap().1.to_vec(),
            },
            Entry { name: "files.json".into(), kind: b'0', body: fj },
        ];
        for (n, b) in sigs {
            entries.push(Entry { name: n.to_string(), kind: b'0', body: b.to_vec() });
        }
        for (p, b) in files {
            if *p != "manifest.toml" {
                entries.push(Entry { name: p.to_string(), kind: b'0', body: b.to_vec() });
            }
        }
        ruzstd::encoding::compress_to_vec(write_tar(&entries).as_slice(), ruzstd::encoding::CompressionLevel::Fastest)
    }

    const MANIFEST: &str = "schema = 1\nid = \"dev.example.w\"\nversion = \"1.0.0\"\nplugin_api = \"1.0\"\nmaktaba = \">=0.6.0, <0.9.0\"\nlicence = \"MIT\"\n\n[strings]\nname = \"plugin.name\"\nsummary = \"plugin.summary\"\n\n[[stage]]\nname = \"s\"\nsubject = \"video\"\nexport = \"run-s\"\nversion = 1\n";

    fn good() -> Vec<(&'static str, Vec<u8>)> {
        vec![
            ("manifest.toml", MANIFEST.as_bytes().to_vec()),
            ("i18n/en.json", br#"{"plugin.name":"W","plugin.summary":"A plugin."}"#.to_vec()),
            ("i18n/ar.json", "{\"plugin.name\":\"و\",\"plugin.summary\":\"إضافة.\"}".as_bytes().to_vec()),
            ("plugin.wasm", b"\0asm".to_vec()),
        ]
    }

    #[test]
    fn inspects_and_repacks() {
        let f = good();
        let files: Vec<(&str, &[u8])> = f.iter().map(|(p, b)| (*p, b.as_slice())).collect();
        let p = pkg(&files, &[(DEVELOPER_SIG, b"dev")]);
        let i = inspect(&p).unwrap();
        assert_eq!(i.manifest["id"], "dev.example.w");
        assert_eq!(i.developer_sig.as_deref(), Some(&b"dev"[..]));
        let r = repack(&p, b"store").unwrap();
        let j = inspect(&r).unwrap();
        assert_eq!((j.developer_sig.as_deref(), j.store_sig.as_deref()), (Some(&b"dev"[..]), Some(&b"store"[..])));
        assert_eq!(j.files_json, i.files_json);
        let names: Vec<_> = j.entries.iter().map(|e| e.name.as_str()).collect();
        assert_eq!(&names[..4], ["manifest.toml", "files.json", DEVELOPER_SIG, STORE_SIG]);
        // a second countersignature replaces the first
        let k = inspect(&repack(&r, b"again").unwrap()).unwrap();
        assert_eq!(k.store_sig.as_deref(), Some(&b"again"[..]));
        assert_eq!(k.entries.iter().filter(|e| e.name == STORE_SIG).count(), 1);
    }

    #[test]
    fn refuses_what_the_home_refuses() {
        let f = good();
        let files: Vec<(&str, &[u8])> = f.iter().map(|(p, b)| (*p, b.as_slice())).collect();
        let mut tampered = files.clone();
        let bad: &[u8] = b"\0asX";
        tampered[3].1 = bad;
        // files.json from the good files, a changed file in the archive
        let good_pkg = read_tar(&decompress(&pkg(&files, &[])).unwrap()).unwrap();
        let mut entries = good_pkg.clone();
        entries.iter_mut().find(|e| e.name == "plugin.wasm").unwrap().body = bad.to_vec();
        let p = ruzstd::encoding::compress_to_vec(
            write_tar(&entries).as_slice(),
            ruzstd::encoding::CompressionLevel::Fastest,
        );
        assert_eq!(inspect(&p).err().unwrap().code, "package_digest_mismatch");
        let mut extra = good_pkg.clone();
        extra.push(Entry { name: "secret.txt".into(), kind: b'0', body: b"x".to_vec() });
        let p = ruzstd::encoding::compress_to_vec(
            write_tar(&extra).as_slice(),
            ruzstd::encoding::CompressionLevel::Fastest,
        );
        assert_eq!(inspect(&p).err().unwrap().code, "package_unlisted_file");
        let mut link = good_pkg.clone();
        link.push(Entry { name: "../escape".into(), kind: b'2', body: vec![] });
        let p =
            ruzstd::encoding::compress_to_vec(write_tar(&link).as_slice(), ruzstd::encoding::CompressionLevel::Fastest);
        assert_eq!(inspect(&p).err().unwrap().code, "package_bad_path");
        assert_eq!(inspect(b"not zstd").err().unwrap().code, "package_bad_head");
        let mut no_ar = files.clone();
        no_ar.retain(|f| f.0 != "i18n/ar.json");
        assert!(inspect(&pkg(&no_ar, &[])).err().unwrap().code.starts_with("manifest_"));
    }

    #[test]
    fn long_paths_round_trip() {
        let name = format!("a/{}/x.txt", "b".repeat(120));
        let e = vec![Entry { name: name.clone(), kind: b'0', body: b"hi".to_vec() }];
        let back = read_tar(&write_tar(&e)).unwrap();
        assert_eq!((back[0].name.as_str(), back[0].body.as_slice()), (name.as_str(), &b"hi"[..]));
    }
}
