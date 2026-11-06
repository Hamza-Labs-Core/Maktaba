// The shared redaction list (shared/redact/rules.json, Epic 53 story 53.01): the rules
// every Maktaba logger uses, included from the original at build time, and the cases
// every logger passes (shared/redact/cases.json, run by the tests below). A
// replacement names its groups as $1, which the regex crate reads as is (never
// followed by a letter, digit or underscore).
use regex::Regex;
use serde_json::Value;
use std::collections::HashSet;
use std::sync::OnceLock;

const RULES_JSON: &str = include_str!("../../../../shared/redact/rules.json");

struct Rule {
    re: Regex,
    replace: String,
    needs: Vec<String>,
    ignore_case: bool,
}

struct Rules {
    key_mask: String,
    keys: HashSet<String>,
    suffixes: Vec<String>,
    parts: Vec<String>,
    values: Vec<Rule>,
    cards: Regex,
    card_first: String,
    card_min: usize,
    card_max: usize,
    card_replace: String,
    ipv4: Regex,
    ipv4_keep: HashSet<String>,
    ipv6: Regex,
}

fn strs(v: &Value, key: &str) -> Vec<String> {
    v[key]
        .as_array()
        .map(|a| {
            a.iter()
                .filter_map(|s| s.as_str().map(String::from))
                .collect()
        })
        .unwrap_or_default()
}

fn rules() -> &'static Rules {
    static RULES: OnceLock<Rules> = OnceLock::new();
    RULES.get_or_init(|| {
        let raw: Value =
            serde_json::from_str(RULES_JSON).expect("shared/redact/rules.json is JSON");
        let re = |p: &Value, ic: bool| {
            let p = p.as_str().unwrap_or_default();
            Regex::new(&if ic {
                format!("(?i){p}")
            } else {
                p.to_string()
            })
            .expect("a shared redaction pattern compiles")
        };
        let values = raw["values"]
            .as_array()
            .map(|a| {
                a.iter()
                    .map(|v| {
                        let ic = v["ignore_case"].as_bool().unwrap_or(false);
                        Rule {
                            re: re(&v["pattern"], ic),
                            replace: v["replace"].as_str().unwrap_or_default().to_string(),
                            needs: strs(v, "needs")
                                .into_iter()
                                .map(|n| if ic { n.to_lowercase() } else { n })
                                .collect(),
                            ignore_case: ic,
                        }
                    })
                    .collect()
            })
            .unwrap_or_default();
        let c = &raw["cards"];
        Rules {
            key_mask: raw["key_mask"]
                .as_str()
                .unwrap_or("***REDACTED***")
                .to_string(),
            keys: strs(&raw, "keys")
                .into_iter()
                .map(|k| k.to_lowercase())
                .collect(),
            suffixes: strs(&raw, "key_suffixes"),
            parts: strs(&raw, "key_parts_string_only"),
            values,
            cards: re(&c["pattern"], false),
            card_first: c["first_digits"].as_str().unwrap_or_default().to_string(),
            card_min: c["min_digits"].as_u64().unwrap_or(15) as usize,
            card_max: c["max_digits"].as_u64().unwrap_or(16) as usize,
            card_replace: c["replace"].as_str().unwrap_or("[CARD]").to_string(),
            ipv4: re(&raw["ipv4"]["pattern"], false),
            ipv4_keep: strs(&raw["ipv4"], "keep_first_octet").into_iter().collect(),
            ipv6: re(&raw["ipv6"]["pattern"], false),
        }
    })
}

/// The mask a whole sensitive field gets.
#[allow(dead_code)]
pub fn key_mask() -> &'static str {
    &rules().key_mask
}

/// Whether a field named `key` is masked whole; `is_string` says whether its value is a string.
#[allow(dead_code)]
pub fn key_sensitive(key: &str, is_string: bool) -> bool {
    let r = rules();
    let low = key.to_lowercase();
    if r.keys.contains(&low) || r.suffixes.iter().any(|s| low.ends_with(s.as_str())) {
        return true;
    }
    is_string && r.parts.iter().any(|p| low.contains(p.as_str()))
}

/// `text` with the shared value rules applied: credentials, emails, home folders, cards, IPs.
pub fn scrub(text: &str) -> String {
    let r = rules();
    let mut out = text.to_string();
    if out.is_empty() {
        return out;
    }
    let mut low = out.to_lowercase();
    for v in &r.values {
        let hay = if v.ignore_case { &low } else { &out };
        if !v.needs.is_empty() && !v.needs.iter().any(|n| hay.contains(n.as_str())) {
            continue;
        }
        let next = v.re.replace_all(&out, v.replace.as_str()).into_owned();
        if next != out {
            out = next;
            low = out.to_lowercase();
        }
    }
    let digits = out.bytes().filter(u8::is_ascii_digit).count();
    if digits >= r.card_min {
        out = replace_checked(&out, &r.cards, card);
    }
    if out.bytes().filter(|b| *b == b'.').count() >= 3 && digits >= 4 {
        out = replace_checked(&out, &r.ipv4, v4);
    }
    if out.bytes().filter(|b| *b == b':').count() >= 3 {
        out = replace_checked(&out, &r.ipv6, v6);
    }
    out
}

fn replace_checked(
    text: &str,
    re: &Regex,
    f: fn(&str, Option<u8>, Option<u8>) -> String,
) -> String {
    let b = text.as_bytes();
    re.replace_all(text, |c: &regex::Captures| {
        let m = c.get(0).expect("the whole match");
        let before = m.start().checked_sub(1).map(|i| b[i]);
        let after = b.get(m.end()).copied();
        f(m.as_str(), before, after)
    })
    .into_owned()
}

fn digit(c: Option<u8>) -> bool {
    c.is_some_and(|c| c.is_ascii_digit())
}

fn hex(c: Option<u8>) -> bool {
    c.is_some_and(|c| c.is_ascii_hexdigit())
}

fn card(m: &str, before: Option<u8>, after: Option<u8>) -> String {
    let r = rules();
    if digit(before) || digit(after) {
        return m.to_string();
    }
    let d: Vec<u32> = m.chars().filter_map(|c| c.to_digit(10)).collect();
    if d.len() < r.card_min
        || d.len() > r.card_max
        || !r
            .card_first
            .contains(char::from_digit(d[0], 10).unwrap_or('x'))
    {
        return m.to_string();
    }
    let sum: u32 = d
        .iter()
        .rev()
        .enumerate()
        .map(|(i, n)| {
            let x = if i % 2 == 1 { n * 2 } else { *n };
            if x > 9 {
                x - 9
            } else {
                x
            }
        })
        .sum();
    if sum % 10 == 0 {
        r.card_replace.clone()
    } else {
        m.to_string()
    }
}

fn v4(m: &str, before: Option<u8>, after: Option<u8>) -> String {
    // part of a longer number (a version like 1.2.3.4.5 is cut from its start only)
    if digit(before) || before == Some(b'.') || digit(after) {
        return m.to_string();
    }
    let p: Vec<&str> = m.split('.').collect();
    if p.iter().any(|x| x.parse::<u32>().map_or(true, |n| n > 255))
        || rules().ipv4_keep.contains(p[0])
    {
        return m.to_string();
    }
    format!("{}.{}.{}.0", p[0], p[1], p[2])
}

fn ipv6_groups(m: &str) -> Option<Vec<String>> {
    if m.contains(":::") || m.matches("::").count() > 1 {
        return None;
    }
    let groups: Vec<String> = if let Some(at) = m.find("::") {
        let (l, r) = (&m[..at], &m[at + 2..]);
        let left: Vec<&str> = if l.is_empty() {
            vec![]
        } else {
            l.split(':').collect()
        };
        let right: Vec<&str> = if r.is_empty() {
            vec![]
        } else {
            r.split(':').collect()
        };
        if left.len() + right.len() > 7 {
            return None;
        }
        let zeros = vec!["0"; 8 - left.len() - right.len()];
        left.into_iter()
            .chain(zeros)
            .chain(right)
            .map(String::from)
            .collect()
    } else {
        let g: Vec<String> = m.split(':').map(String::from).collect();
        if g.len() != 8 {
            return None;
        }
        g
    };
    if groups.iter().any(String::is_empty) {
        None
    } else {
        Some(groups)
    }
}

fn v6(m: &str, before: Option<u8>, after: Option<u8>) -> String {
    if hex(before) || before == Some(b':') || hex(after) || after == Some(b':') {
        return m.to_string();
    }
    let Some(g) = ipv6_groups(m) else {
        return m.to_string();
    };
    let n = |s: &str| u16::from_str_radix(s, 16).unwrap_or(u16::MAX);
    if g[..7].iter().all(|x| n(x) == 0) && n(&g[7]) == 1 {
        return m.to_string();
    }
    format!("{}:{}:{}::", g[0], g[1], g[2])
}

#[cfg(test)]
mod tests {
    use super::*;

    const CASES: &str = include_str!("../../../../shared/redact/cases.json");

    #[test]
    fn every_shared_case_passes() {
        let cases: Value = serde_json::from_str(CASES).unwrap();
        for c in cases["scrub"].as_array().unwrap() {
            let input = c["in"].as_str().unwrap();
            assert_eq!(scrub(input), c["out"].as_str().unwrap(), "scrub({input:?})");
        }
        for c in cases["keys"].as_array().unwrap() {
            let key = c["key"].as_str().unwrap();
            assert_eq!(
                key_sensitive(key, c["string"].as_bool().unwrap()),
                c["masked"].as_bool().unwrap(),
                "{key}"
            );
        }
        assert_eq!(key_mask(), "***REDACTED***");
    }
}
