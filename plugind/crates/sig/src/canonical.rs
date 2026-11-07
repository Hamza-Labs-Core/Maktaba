//! Canonical JSON (story 54.14 §2), byte for byte the same as Go's
//! shared/plugins/pluginsig/canonical.go and TypeScript's store/signing/canonical.ts:
//! keys sorted by their UTF-8 bytes and never twice, no spaces, strings escaped only where
//! they must be, integers within ±(2^53 − 1) only, no surrogate escapes.

use std::collections::BTreeMap;

/// A parsed value. Objects are a BTreeMap, whose String keys sort by their bytes.
#[derive(Debug, Clone, PartialEq)]
pub enum Value {
    Null,
    Bool(bool),
    Int(i64),
    Str(String),
    Arr(Vec<Value>),
    Obj(BTreeMap<String, Value>),
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct CanonicalError(pub String);

impl std::fmt::Display for CanonicalError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "not canonical JSON: {}", self.0)
    }
}

impl std::error::Error for CanonicalError {}

fn err<T>(m: impl Into<String>) -> Result<T, CanonicalError> {
    Err(CanonicalError(m.into()))
}

const MAX_SAFE: i64 = (1 << 53) - 1;

struct Parser<'a> {
    s: &'a [u8],
    i: usize,
}

impl Parser<'_> {
    fn ws(&mut self) {
        while self.i < self.s.len() && matches!(self.s[self.i], b' ' | b'\t' | b'\n' | b'\r') {
            self.i += 1;
        }
    }

    fn value(&mut self) -> Result<Value, CanonicalError> {
        self.ws();
        match self.s.get(self.i) {
            Some(b'{') => {
                self.i += 1;
                let mut m = BTreeMap::new();
                self.ws();
                if self.s.get(self.i) == Some(&b'}') {
                    self.i += 1;
                    return Ok(Value::Obj(m));
                }
                loop {
                    self.ws();
                    if self.s.get(self.i) != Some(&b'"') {
                        return err("a key is not a string");
                    }
                    let k = self.string()?;
                    if m.contains_key(&k) {
                        return err(format!("the key {k:?} comes twice"));
                    }
                    self.ws();
                    if self.s.get(self.i) != Some(&b':') {
                        return err("expected ':'");
                    }
                    self.i += 1;
                    let v = self.value()?;
                    m.insert(k, v);
                    self.ws();
                    match self.s.get(self.i) {
                        Some(b',') => self.i += 1,
                        Some(b'}') => {
                            self.i += 1;
                            return Ok(Value::Obj(m));
                        }
                        _ => return err("expected ',' or '}'"),
                    }
                }
            }
            Some(b'[') => {
                self.i += 1;
                let mut a = Vec::new();
                self.ws();
                if self.s.get(self.i) == Some(&b']') {
                    self.i += 1;
                    return Ok(Value::Arr(a));
                }
                loop {
                    a.push(self.value()?);
                    self.ws();
                    match self.s.get(self.i) {
                        Some(b',') => self.i += 1,
                        Some(b']') => {
                            self.i += 1;
                            return Ok(Value::Arr(a));
                        }
                        _ => return err("expected ',' or ']'"),
                    }
                }
            }
            Some(b'"') => Ok(Value::Str(self.string()?)),
            Some(_) if self.s[self.i..].starts_with(b"true") => {
                self.i += 4;
                Ok(Value::Bool(true))
            }
            Some(_) if self.s[self.i..].starts_with(b"false") => {
                self.i += 5;
                Ok(Value::Bool(false))
            }
            Some(_) if self.s[self.i..].starts_with(b"null") => {
                self.i += 4;
                Ok(Value::Null)
            }
            Some(_) => self.number(),
            None => err("unexpected end"),
        }
    }

    fn number(&mut self) -> Result<Value, CanonicalError> {
        let start = self.i;
        while self.i < self.s.len() && matches!(self.s[self.i], b'-' | b'+' | b'.' | b'e' | b'E' | b'0'..=b'9') {
            self.i += 1;
        }
        let t = std::str::from_utf8(&self.s[start..self.i]).unwrap_or("");
        let digits = t.strip_prefix('-').unwrap_or(t);
        let ok = !digits.is_empty()
            && digits.bytes().all(|c| c.is_ascii_digit())
            && (digits == "0" || !digits.starts_with('0'))
            && t != "-0";
        if !ok {
            return err(format!("{t} is not an integer"));
        }
        match t.parse::<i64>() {
            Ok(n) if (-MAX_SAFE..=MAX_SAFE).contains(&n) => Ok(Value::Int(n)),
            _ => err(format!("{t} is out of range")),
        }
    }

    fn hex4(&mut self) -> Result<u32, CanonicalError> {
        let h = self.s.get(self.i..self.i + 4).ok_or_else(|| CanonicalError("a bad \\u escape".into()))?;
        let h = std::str::from_utf8(h).map_err(|_| CanonicalError("a bad \\u escape".into()))?;
        if !h.bytes().all(|c| c.is_ascii_hexdigit()) {
            return err("a bad \\u escape");
        }
        self.i += 4;
        Ok(u32::from_str_radix(h, 16).unwrap_or(0))
    }

    fn string(&mut self) -> Result<String, CanonicalError> {
        self.i += 1;
        let mut out: Vec<u8> = Vec::new();
        loop {
            let Some(&c) = self.s.get(self.i) else {
                return err("an unterminated string");
            };
            match c {
                b'"' => {
                    self.i += 1;
                    return String::from_utf8(out).map_err(|_| CanonicalError("a string is not UTF-8".into()));
                }
                0..=0x1f => return err("a control character in a string"),
                b'\\' => {
                    let e = self.s.get(self.i + 1).copied();
                    self.i += 2;
                    let ch = match e {
                        Some(b'"') => '"',
                        Some(b'\\') => '\\',
                        Some(b'/') => '/',
                        Some(b'b') => '\u{8}',
                        Some(b'f') => '\u{c}',
                        Some(b'n') => '\n',
                        Some(b'r') => '\r',
                        Some(b't') => '\t',
                        Some(b'u') => {
                            let n = self.hex4()?;
                            if (0xd800..=0xdfff).contains(&n) {
                                return err("a surrogate escape");
                            }
                            char::from_u32(n).ok_or_else(|| CanonicalError("a bad \\u escape".into()))?
                        }
                        _ => return err("a bad escape"),
                    };
                    let mut buf = [0u8; 4];
                    out.extend_from_slice(ch.encode_utf8(&mut buf).as_bytes());
                }
                _ => {
                    out.push(c);
                    self.i += 1;
                }
            }
        }
    }
}

/// Reads one JSON value strictly.
pub fn parse(raw: &[u8]) -> Result<Value, CanonicalError> {
    if std::str::from_utf8(raw).is_err() {
        return err("the text is not UTF-8");
    }
    let mut p = Parser { s: raw, i: 0 };
    let v = p.value()?;
    p.ws();
    if p.i != raw.len() {
        return err("data after the value");
    }
    Ok(v)
}

fn write_str(out: &mut Vec<u8>, s: &str) {
    out.push(b'"');
    for &c in s.as_bytes() {
        match c {
            b'"' => out.extend_from_slice(b"\\\""),
            b'\\' => out.extend_from_slice(b"\\\\"),
            0x08 => out.extend_from_slice(b"\\b"),
            0x0c => out.extend_from_slice(b"\\f"),
            b'\n' => out.extend_from_slice(b"\\n"),
            b'\r' => out.extend_from_slice(b"\\r"),
            b'\t' => out.extend_from_slice(b"\\t"),
            0..=0x1f => out.extend_from_slice(format!("\\u{c:04x}").as_bytes()),
            _ => out.push(c),
        }
    }
    out.push(b'"');
}

/// Writes a value canonically.
pub fn write(out: &mut Vec<u8>, v: &Value) {
    match v {
        Value::Null => out.extend_from_slice(b"null"),
        Value::Bool(true) => out.extend_from_slice(b"true"),
        Value::Bool(false) => out.extend_from_slice(b"false"),
        Value::Int(n) => out.extend_from_slice(n.to_string().as_bytes()),
        Value::Str(s) => write_str(out, s),
        Value::Arr(a) => {
            out.push(b'[');
            for (i, e) in a.iter().enumerate() {
                if i > 0 {
                    out.push(b',');
                }
                write(out, e);
            }
            out.push(b']');
        }
        Value::Obj(m) => {
            out.push(b'{');
            for (i, (k, e)) in m.iter().enumerate() {
                if i > 0 {
                    out.push(b',');
                }
                write_str(out, k);
                out.push(b':');
                write(out, e);
            }
            out.push(b'}');
        }
    }
}

/// The canonical bytes of a JSON text.
pub fn canonical(raw: &[u8]) -> Result<Vec<u8>, CanonicalError> {
    let v = parse(raw)?;
    let mut out = Vec::new();
    write(&mut out, &v);
    Ok(out)
}

/// The canonical bytes of a serde value (its JSON through serde_json, then canonical).
pub fn canonical_of<T: serde::Serialize>(v: &T) -> Result<Vec<u8>, CanonicalError> {
    let raw = serde_json::to_vec(v).map_err(|e| CanonicalError(e.to_string()))?;
    canonical(&raw)
}
