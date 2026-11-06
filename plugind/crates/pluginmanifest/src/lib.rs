//! The plugin manifest validator in Rust (Epic 54, story 54.01): the schema in
//! `shared/plugins/manifest.schema.json`, then the same rules across fields in the same
//! order as the Go, Python and TypeScript validators, and the same normalised JSON byte
//! for byte (the corpus in `shared/plugins/testdata/manifests`).
//!
//! The host (54.02) and the SDK's `mkp build` (54.12) read manifests with it.

use std::collections::BTreeMap;
use std::sync::OnceLock;

use regex::Regex;
use serde::Deserialize;
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};

pub const MANIFEST_SCHEMA: &str = include_str!("../../../../shared/plugins/manifest.schema.json");
pub const CAPABILITIES_TOML: &str = include_str!("../../../../shared/plugins/capabilities.toml");
pub const EVENTS_TOML: &str = include_str!("../../../../shared/plugins/events.toml");
pub const WIT: &str = include_str!("../../../../shared/plugins/wit/maktaba-plugin.wit");

pub const CODE_INVALID: &str = "manifest_invalid";
pub const CODE_UNKNOWN_KEY: &str = "manifest_unknown_key";
pub const CODE_MISSING_KEY: &str = "manifest_missing_key";
pub const CODE_SCHEMA_NEWER: &str = "manifest_schema_newer";
pub const CODE_RESERVED_ID: &str = "manifest_reserved_id";
pub const CODE_BAD_PLATFORM: &str = "manifest_bad_platform";
pub const CODE_BAD_RANGE: &str = "manifest_bad_range";
pub const CODE_BAD_CAPABILITY: &str = "manifest_bad_capability";
pub const CODE_DUPLICATE_CAPABILITY: &str = "manifest_duplicate_capability";
pub const CODE_DUPLICATE_STAGE: &str = "manifest_duplicate_stage";
pub const CODE_BAD_REQUEST: &str = "manifest_bad_request";
pub const CODE_UNKNOWN_EVENT: &str = "manifest_unknown_event";
pub const CODE_SUBJECT_MISMATCH: &str = "manifest_subject_mismatch";
pub const CODE_BAD_SCHEDULE: &str = "manifest_bad_schedule";
pub const CODE_MISSING_STRING: &str = "manifest_missing_string";
pub const CODE_STRING_TOO_LONG: &str = "manifest_string_too_long";
pub const CODE_MISSING_FILE: &str = "manifest_missing_file";
pub const CODE_BAD_SETTINGS: &str = "manifest_bad_settings";
pub const CODE_NOT_TOML: &str = "manifest_not_toml";

const SCHEMA_VERSION: i64 = 1;
const MAX_NAME: usize = 40;
const MAX_SUMMARY: usize = 140;

const KEYWORDS: &[&str] = &[
    "$schema",
    "$id",
    "title",
    "description",
    "type",
    "properties",
    "additionalProperties",
    "required",
    "enum",
    "pattern",
    "minLength",
    "maxLength",
    "minimum",
    "maximum",
    "items",
    "minItems",
    "maxItems",
    "uniqueItems",
    "default",
    "x-code",
    "x-unknown-code",
];

/// One refusal: a code, a slash path into the manifest and a detail (a language, a file).
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ManifestError {
    pub code: String,
    pub path: String,
    pub detail: String,
}

impl ManifestError {
    fn new(code: &str, path: &str, detail: &str) -> Self {
        Self { code: code.into(), path: path.into(), detail: detail.into() }
    }
}

impl std::fmt::Display for ManifestError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{} at {}: {}", self.code, self.path, self.detail)
    }
}

/// One entry of capabilities.toml.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Capability {
    pub name: String,
    pub risk: String,
    pub interface: String,
    pub functions: Vec<String>,
    #[serde(default)]
    pub param: Option<String>,
    pub title_key: String,
    pub detail_key: String,
    pub since: String,
}

/// One entry of events.toml.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Event {
    pub name: String,
    pub subject: String,
    pub stage: String,
    pub map: String,
    #[serde(default)]
    pub trigger: Option<bool>,
    pub since: String,
}

struct Loaded {
    schema: Value,
    caps: BTreeMap<String, Capability>,
    events: BTreeMap<String, Event>,
    regexes: BTreeMap<String, Regex>,
}

fn loaded() -> &'static Loaded {
    static L: OnceLock<Loaded> = OnceLock::new();
    L.get_or_init(|| {
        let schema: Value = serde_json::from_str(MANIFEST_SCHEMA).expect("manifest.schema.json");
        let mut regexes = BTreeMap::new();
        fn walk(n: &Value, regexes: &mut BTreeMap<String, Regex>) {
            let o = n.as_object().expect("schema node");
            for k in o.keys() {
                assert!(KEYWORDS.contains(&k.as_str()), "schema keyword {k} is not read by the validators");
            }
            if let Some(p) = o.get("pattern").and_then(Value::as_str) {
                regexes.insert(p.to_string(), Regex::new(p).expect("schema pattern"));
            }
            if let Some(props) = o.get("properties").and_then(Value::as_object) {
                for c in props.values() {
                    walk(c, regexes);
                }
            }
            if let Some(items) = o.get("items") {
                walk(items, regexes);
            }
        }
        walk(&schema, &mut regexes);
        #[derive(Deserialize)]
        struct Caps {
            #[allow(dead_code)]
            schema: i64,
            capability: Vec<Capability>,
        }
        #[derive(Deserialize)]
        struct Evs {
            #[allow(dead_code)]
            schema: i64,
            event: Vec<Event>,
        }
        let caps: Caps = toml::from_str(CAPABILITIES_TOML).expect("capabilities.toml");
        let evs: Evs = toml::from_str(EVENTS_TOML).expect("events.toml");
        Loaded {
            schema,
            caps: caps.capability.into_iter().map(|c| (c.name.clone(), c)).collect(),
            events: evs.event.into_iter().map(|e| (e.name.clone(), e)).collect(),
            regexes,
        }
    })
}

/// capabilities.toml.
pub fn capabilities() -> impl Iterator<Item = &'static Capability> {
    loaded().caps.values()
}

/// The entry of a granted name ("plugin-data.read:x.y.z" finds "plugin-data.read").
pub fn lookup_capability(name: &str) -> Option<&'static Capability> {
    loaded().caps.get(name.split(':').next().unwrap_or(""))
}

/// events.toml.
pub fn events() -> impl Iterator<Item = &'static Event> {
    loaded().events.values()
}

pub fn valid_id(id: &str) -> bool {
    let l = loaded();
    let p = l.schema["properties"]["id"]["pattern"].as_str().unwrap_or("");
    id.len() <= 100 && l.regexes[p].is_match(id)
}

/// A plugin stage's `job_stages.name` (54.01 §4): `p_<8 hex of sha256(id)>_<stage>`.
pub fn queue_name(plugin_id: &str, stage: &str) -> String {
    let h = hex::encode(Sha256::digest(plugin_id.as_bytes()));
    format!("p_{}_{}", &h[..8], stage)
}

/// The package's files the package rules read (i18n, ui), by package path.
pub trait Files {
    fn get(&self, path: &str) -> Option<Vec<u8>>;
}

impl<F: Fn(&str) -> Option<Vec<u8>>> Files for F {
    fn get(&self, path: &str) -> Option<Vec<u8>> {
        self(path)
    }
}

/// Parse and check a manifest.toml: the normalised manifest, or every refusal.
/// `files: None` skips the package rules (a manifest checked on its own).
pub fn validate(src: &[u8], files: Option<&dyn Files>) -> Result<Map<String, Value>, Vec<ManifestError>> {
    let l = loaded();
    let raw = parse_toml(src).map_err(|e| vec![ManifestError::new(CODE_NOT_TOML, "", &e)])?;
    if let Some(s) = raw.get("schema").and_then(Value::as_i64) {
        if raw["schema"].is_i64() && s > SCHEMA_VERSION {
            return Err(vec![ManifestError::new(CODE_SCHEMA_NEWER, "schema", "")]);
        }
    }
    let mut v = Value::Object(raw);
    let mut errs = Vec::new();
    check(&l.schema, &v, "", "", &mut errs);
    if !errs.is_empty() {
        return Err(errs);
    }
    fill(&l.schema, &mut v);
    let Value::Object(mut m) = v else { unreachable!() };
    let range =
        m["maktaba"].as_str().unwrap_or("").split(',').map(|p| p.replace(' ', "")).collect::<Vec<_>>().join(", ");
    m.insert("maktaba".into(), Value::String(range));
    let errs = rules(&m, files);
    if !errs.is_empty() {
        return Err(errs);
    }
    Ok(m)
}

/// The one JSON text of a valid manifest: keys sorted, no spaces, UTF-8. Sorted here, not
/// by serde_json's map, whose order depends on a feature another crate may switch on.
pub fn normalised(m: &Map<String, Value>) -> String {
    let mut out = String::new();
    canonical(&Value::Object(m.clone()), &mut out);
    out
}

fn canonical(v: &Value, out: &mut String) {
    match v {
        Value::Object(o) => {
            let mut keys: Vec<&String> = o.keys().collect();
            keys.sort();
            out.push('{');
            for (i, k) in keys.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                out.push_str(&serde_json::to_string(k).expect("json"));
                out.push(':');
                canonical(&o[k.as_str()], out);
            }
            out.push('}');
        }
        Value::Array(a) => {
            out.push('[');
            for (i, x) in a.iter().enumerate() {
                if i > 0 {
                    out.push(',');
                }
                canonical(x, out);
            }
            out.push(']');
        }
        _ => out.push_str(&serde_json::to_string(v).expect("json")),
    }
}

fn parse_toml(src: &[u8]) -> Result<Map<String, Value>, String> {
    if src.len() > 1 << 20 {
        return Err("over 1 MB".into());
    }
    let text = std::str::from_utf8(src).map_err(|e| e.to_string())?;
    let t: toml::Table = toml::from_str(text).map_err(|e| e.to_string())?;
    match from_toml(toml::Value::Table(t))? {
        Value::Object(m) => Ok(m),
        _ => Err("not a table".into()),
    }
}

fn from_toml(v: toml::Value) -> Result<Value, String> {
    Ok(match v {
        toml::Value::String(s) => Value::String(s),
        toml::Value::Integer(i) => Value::from(i),
        toml::Value::Float(f) => serde_json::Number::from_f64(f).map(Value::Number).ok_or("not a finite number")?,
        toml::Value::Boolean(b) => Value::Bool(b),
        toml::Value::Datetime(_) => return Err("a date or time is not allowed in a manifest".into()),
        toml::Value::Array(a) => Value::Array(a.into_iter().map(from_toml).collect::<Result<_, _>>()?),
        toml::Value::Table(t) => {
            let mut m = Map::new();
            for (k, x) in t {
                m.insert(k, from_toml(x)?);
            }
            Value::Object(m)
        }
    })
}

fn type_of(v: &Value) -> &'static str {
    match v {
        Value::Null => "null",
        Value::Bool(_) => "boolean",
        Value::Number(n) if n.is_i64() || n.is_u64() => "integer",
        Value::Number(_) => "number",
        Value::String(_) => "string",
        Value::Array(_) => "array",
        Value::Object(_) => "object",
    }
}

fn as_f64(v: &Value) -> f64 {
    v.as_f64().unwrap_or(0.0)
}

fn equal(a: &Value, b: &Value) -> bool {
    match (a, b) {
        (Value::Number(x), Value::Number(y)) => x.as_f64() == y.as_f64(),
        _ => a == b,
    }
}

fn join(path: &str, el: &str) -> String {
    if path.is_empty() {
        el.to_string()
    } else {
        format!("{path}/{el}")
    }
}

fn check(n: &Value, v: &Value, path: &str, parent: &str, errs: &mut Vec<ManifestError>) {
    let l = loaded();
    let code = n.get("x-code").and_then(Value::as_str).filter(|s| !s.is_empty()).unwrap_or(if parent.is_empty() {
        CODE_INVALID
    } else {
        parent
    });
    let t = type_of(v);
    if let Some(types) = n.get("type") {
        let wants: Vec<&str> = match types {
            Value::String(s) => vec![s.as_str()],
            Value::Array(a) => a.iter().filter_map(Value::as_str).collect(),
            _ => vec![],
        };
        if !wants.iter().any(|w| *w == t || (*w == "number" && t == "integer")) {
            errs.push(ManifestError::new(code, path, &format!("type {t}")));
            return;
        }
    }
    if let Some(Value::Array(en)) = n.get("enum") {
        if !en.iter().any(|e| equal(e, v)) {
            errs.push(ManifestError::new(code, path, "not one of the allowed values"));
            return;
        }
    }
    let num = |k: &str| n.get(k).and_then(Value::as_f64);
    match v {
        Value::String(s) => {
            let count = s.chars().count() as f64;
            if num("minLength").is_some_and(|m| count < m) || num("maxLength").is_some_and(|m| count > m) {
                errs.push(ManifestError::new(code, path, "length"));
                return;
            }
            if let Some(p) = n.get("pattern").and_then(Value::as_str) {
                if !l.regexes[p].is_match(s) {
                    errs.push(ManifestError::new(code, path, "pattern"));
                }
            }
        }
        Value::Number(_) => {
            let x = as_f64(v);
            if num("minimum").is_some_and(|m| x < m) || num("maximum").is_some_and(|m| x > m) {
                errs.push(ManifestError::new(code, path, "range"));
            }
        }
        Value::Array(a) => {
            let len = a.len() as f64;
            if num("minItems").is_some_and(|m| len < m) || num("maxItems").is_some_and(|m| len > m) {
                errs.push(ManifestError::new(code, path, "items"));
                return;
            }
            if n.get("uniqueItems").and_then(Value::as_bool) == Some(true) {
                for i in 0..a.len() {
                    for j in 0..i {
                        if equal(&a[i], &a[j]) {
                            errs.push(ManifestError::new(code, path, "duplicate item"));
                            return;
                        }
                    }
                }
            }
            if let Some(items) = n.get("items") {
                for (i, x) in a.iter().enumerate() {
                    check(items, x, &join(path, &i.to_string()), code, errs);
                }
            }
        }
        Value::Object(o) => {
            if let Some(Value::Array(req)) = n.get("required") {
                for r in req.iter().filter_map(Value::as_str) {
                    if !o.contains_key(r) {
                        errs.push(ManifestError::new(CODE_MISSING_KEY, &join(path, r), ""));
                    }
                }
            }
            let props = n.get("properties").and_then(Value::as_object);
            for (k, x) in o {
                match props.and_then(|p| p.get(k)) {
                    Some(c) => check(c, x, &join(path, k), code, errs),
                    None => {
                        if n.get("additionalProperties") == Some(&Value::Bool(false)) {
                            errs.push(ManifestError::new(CODE_UNKNOWN_KEY, &join(path, k), ""));
                        }
                    }
                }
            }
        }
        _ => {}
    }
}

fn fill(n: &Value, v: &mut Value) {
    match v {
        Value::Object(o) => {
            if let Some(props) = n.get("properties").and_then(Value::as_object) {
                for (k, c) in props {
                    if !o.contains_key(k) {
                        if let Some(d) = c.get("default") {
                            o.insert(k.clone(), d.clone());
                        }
                    }
                    if let Some(x) = o.get_mut(k) {
                        fill(c, x);
                    }
                }
            }
        }
        Value::Array(a) => {
            if let Some(items) = n.get("items") {
                for x in a {
                    fill(items, x);
                }
            }
        }
        _ => {}
    }
}

fn reserved(id: &str) -> bool {
    let mut labels: Vec<&str> = id.split('.').collect();
    if id.starts_with("tv.maktaba.community.") {
        labels = labels[3..].to_vec();
    } else if id.starts_with("tv.maktaba.") || id.starts_with("dev.maktaba.") {
        return true;
    }
    labels.iter().any(|l| *l == "maktaba" || *l == "official")
}

fn known_capability(name: &str) -> bool {
    let (base, param) = match name.split_once(':') {
        Some((b, p)) => (b, Some(p)),
        None => (name, None),
    };
    let Some(c) = loaded().caps.get(base) else { return false };
    match (&c.param, param) {
        (None, None) => true,
        (Some(_), Some(p)) => valid_id(p),
        _ => false,
    }
}

fn s<'a>(v: &'a Value, k: &str) -> &'a str {
    v.get(k).and_then(Value::as_str).unwrap_or("")
}

fn arr<'a>(v: &'a Value, k: &str) -> &'a [Value] {
    v.get(k).and_then(Value::as_array).map(Vec::as_slice).unwrap_or(&[])
}

fn rules(m: &Map<String, Value>, files: Option<&dyn Files>) -> Vec<ManifestError> {
    let l = loaded();
    let mv = Value::Object(m.clone());
    let mut errs = Vec::new();
    if reserved(s(&mv, "id")) {
        errs.push(ManifestError::new(CODE_RESERVED_ID, "id", ""));
    }
    let platforms = arr(&mv, "platforms");
    if platforms.len() > 1 && platforms.iter().any(|p| p == "any") {
        errs.push(ManifestError::new(CODE_BAD_PLATFORM, "platforms", "any stands alone"));
    }
    if !s(&mv, "maktaba").split(", ").any(|p| p.starts_with('>') || p.starts_with('=')) {
        errs.push(ManifestError::new(CODE_BAD_RANGE, "maktaba", "no lower bound"));
    }
    let caps = &mv["capabilities"];
    let mut seen = std::collections::BTreeSet::new();
    for list in ["required", "optional"] {
        for (i, c) in arr(caps, list).iter().enumerate() {
            let name = c.as_str().unwrap_or("");
            let path = format!("capabilities/{list}/{i}");
            if !known_capability(name) {
                errs.push(ManifestError::new(CODE_BAD_CAPABILITY, &path, name));
                continue;
            }
            if !seen.insert(name.to_string()) {
                errs.push(ManifestError::new(CODE_DUPLICATE_CAPABILITY, &path, name));
            }
        }
    }
    let stages = arr(&mv, "stage");
    let mut subjects: BTreeMap<String, String> = BTreeMap::new();
    for (i, st) in stages.iter().enumerate() {
        let name = s(st, "name");
        if subjects.contains_key(name) {
            errs.push(ManifestError::new(CODE_DUPLICATE_STAGE, &format!("stage/{i}/name"), name));
        }
        subjects.insert(name.into(), s(st, "subject").into());
    }
    for (i, st) in stages.iter().enumerate() {
        for (j, r) in arr(st, "may_request").iter().enumerate() {
            let r = r.as_str().unwrap_or("");
            if !subjects.contains_key(r) {
                errs.push(ManifestError::new(CODE_BAD_REQUEST, &format!("stage/{i}/may_request/{j}"), r));
            }
        }
        for (j, t) in arr(st, "trigger").iter().enumerate() {
            let ev = s(t, "event");
            let path = format!("stage/{i}/trigger/{j}/event");
            match l.events.get(ev) {
                Some(e) if e.trigger != Some(false) => {
                    if e.subject != s(st, "subject") {
                        errs.push(ManifestError::new(CODE_SUBJECT_MISMATCH, &path, ev));
                    }
                }
                _ => errs.push(ManifestError::new(CODE_UNKNOWN_EVENT, &path, ev)),
            }
        }
    }
    for (i, sc) in arr(&mv, "schedule").iter().enumerate() {
        let stage = s(sc, "stage");
        if subjects.get(stage).map(String::as_str) != Some("catalog") {
            errs.push(ManifestError::new(CODE_BAD_SCHEDULE, &format!("schedule/{i}/stage"), stage));
        }
    }
    match files {
        Some(f) if errs.is_empty() => package_rules(&mv, f),
        _ => errs,
    }
}

fn package_rules(m: &Value, files: &dyn Files) -> Vec<ManifestError> {
    let mut errs = Vec::new();
    for lang in ["en", "ar"] {
        let path = format!("i18n/{lang}.json");
        let Some(raw) = files.get(&path) else {
            errs.push(ManifestError::new(CODE_MISSING_FILE, "strings", &path));
            continue;
        };
        let Ok(Value::Object(words)) = serde_json::from_slice::<Value>(&raw) else {
            errs.push(ManifestError::new(CODE_MISSING_STRING, "strings", lang));
            continue;
        };
        for (key, most) in [("name", MAX_NAME), ("summary", MAX_SUMMARY)] {
            match words.get(s(&m["strings"], key)).and_then(Value::as_str) {
                Some(w) if !w.trim().is_empty() => {
                    if w.chars().count() > most {
                        errs.push(ManifestError::new(CODE_STRING_TOO_LONG, &format!("strings/{key}"), lang));
                    }
                }
                _ => errs.push(ManifestError::new(CODE_MISSING_STRING, &format!("strings/{key}"), lang)),
            }
        }
    }
    let mut props = Map::new();
    let settings = m.get("settings");
    if let Some(set) = settings {
        let path = s(set, "schema");
        match files.get(path) {
            None => errs.push(ManifestError::new(CODE_MISSING_FILE, "settings/schema", path)),
            Some(raw) => match serde_json::from_slice::<Value>(&raw) {
                Ok(Value::Object(doc)) => {
                    if let Some(Value::Object(p)) = doc.get("properties") {
                        props = p.clone();
                    }
                }
                _ => errs.push(ManifestError::new(CODE_BAD_SETTINGS, "settings/schema", path)),
            },
        }
    }
    for (i, sc) in arr(m, "schedule").iter().enumerate() {
        let field = s(sc, "every_minutes_setting");
        let path = format!("schedule/{i}/every_minutes_setting");
        if settings.is_none() {
            errs.push(ManifestError::new(CODE_BAD_SCHEDULE, &path, "no settings"));
            continue;
        }
        let Some(p @ Value::Object(_)) = props.get(field) else {
            errs.push(ManifestError::new(CODE_BAD_SCHEDULE, &path, field));
            continue;
        };
        let low = p.get("minimum").and_then(Value::as_f64);
        if p.get("type").and_then(Value::as_str) != Some("integer") || low.is_none_or(|x| x < 15.0) {
            errs.push(ManifestError::new(CODE_BAD_SCHEDULE, &path, field));
        }
    }
    errs
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::path::{Path, PathBuf};

    fn corpus() -> PathBuf {
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../../shared/plugins/testdata/manifests")
    }

    fn package_files() -> impl Fn(&str) -> Option<Vec<u8>> {
        let root = corpus().join("package");
        move |p: &str| std::fs::read(root.join(p)).ok()
    }

    fn tomls(dir: &str) -> Vec<PathBuf> {
        let mut v: Vec<_> = std::fs::read_dir(corpus().join(dir))
            .unwrap()
            .map(|e| e.unwrap().path())
            .filter(|p| p.extension().is_some_and(|e| e == "toml"))
            .collect();
        v.sort();
        assert!(!v.is_empty());
        v
    }

    /// T01 + T02 (rust): valid cases normalise to the goldens byte for byte.
    #[test]
    fn valid_corpus() {
        let files = package_files();
        for p in tomls("valid") {
            let m = validate(&std::fs::read(&p).unwrap(), Some(&files))
                .unwrap_or_else(|e| panic!("{}: {e:?}", p.display()));
            let golden = corpus().join("normalised").join(p.file_stem().unwrap()).with_extension("json");
            let want = std::fs::read_to_string(golden).unwrap();
            assert_eq!(normalised(&m), want.trim_end(), "{}", p.display());
        }
    }

    /// T01 (rust): each invalid case raises its named code.
    #[test]
    fn invalid_corpus() {
        let files = package_files();
        for p in tomls("invalid") {
            let src = std::fs::read_to_string(&p).unwrap();
            let first = src.lines().next().unwrap().trim_start_matches("# expect: ").trim();
            let (code, detail) = first.split_once(' ').unwrap_or((first, ""));
            let errs = validate(src.as_bytes(), Some(&files)).expect_err(&p.display().to_string());
            assert!(
                errs.iter().any(|e| e.code == code && (detail.is_empty() || e.detail == detail)),
                "{}: want {code} {detail}, got {errs:?}",
                p.display()
            );
        }
    }

    #[test]
    fn queue_name_vectors() {
        let raw = std::fs::read_to_string(corpus().join("../stage_names.json")).unwrap();
        let v: Vec<Value> = serde_json::from_str(&raw).unwrap();
        for x in v {
            assert_eq!(queue_name(s(&x, "id"), s(&x, "stage")), s(&x, "name"));
        }
    }

    #[test]
    fn floats_are_not_integers() {
        let src = std::fs::read_to_string(corpus().join("valid/minimal.toml"))
            .unwrap()
            .replace("version = 3", "version = 3.0");
        let errs = validate(src.as_bytes(), None).unwrap_err();
        assert!(errs.iter().any(|e| e.code == "manifest_bad_stage"), "{errs:?}");
    }
}
