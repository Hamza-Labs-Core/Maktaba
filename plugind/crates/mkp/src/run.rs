//! `mkp run <folder> --home <url> --user <name>` (Epic 54, story 54.12 §2): packs the folder,
//! signs in to the home as its admin and installs the package there in developer mode
//! (marked Development, turned on), over the same admin API the Plugins page uses. The
//! home's admin must have allowed unreviewed plugins. Run it again after a change: the same
//! plugin installs as its update, the running version drained first.
//!
//! It speaks plain HTTP to the home's address on the local network (no TLS here); a home
//! reached only over HTTPS takes `maktaba-server plugins dev <folder>` on the home itself.
//! The password is read from MKP_HOME_PASSWORD, or asked on the terminal.

use std::io::{BufRead, Read, Write};
use std::net::TcpStream;
use std::path::Path;
use std::time::Duration;

/// A home's address: host and port.
pub fn parse_home(url: &str) -> Result<(String, String), String> {
    if url.starts_with("https://") {
        return Err("mkp run speaks HTTP to the home's address on your network (http://<home>:<port>); \
                    over HTTPS, use `maktaba-server plugins dev <folder>` on the home"
            .into());
    }
    let rest = url.strip_prefix("http://").ok_or_else(|| format!("--home {url}: http://<home>:<port>"))?;
    let host = rest.split('/').next().unwrap_or_default();
    if host.is_empty() {
        return Err(format!("--home {url}: no host"));
    }
    let addr = if host.contains(':') { host.to_string() } else { format!("{host}:80") };
    Ok((addr, host.to_string()))
}

pub struct Response {
    pub status: u16,
    pub headers: Vec<(String, String)>,
    pub body: Vec<u8>,
}

/// One HTTP/1.1 request on its own connection.
pub fn request(
    addr: &str,
    host: &str,
    method: &str,
    path: &str,
    headers: &[(String, String)],
    body: &[u8],
) -> Result<Response, String> {
    let mut s = TcpStream::connect(addr).map_err(|e| format!("{addr}: {e}"))?;
    s.set_read_timeout(Some(Duration::from_secs(600))).ok(); // an install's preload compiles
    let mut head =
        format!("{method} {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\nContent-Length: {}\r\n", body.len());
    for (k, v) in headers {
        head.push_str(&format!("{k}: {v}\r\n"));
    }
    head.push_str("\r\n");
    s.write_all(head.as_bytes()).and_then(|_| s.write_all(body)).map_err(|e| e.to_string())?;
    let mut raw = Vec::new();
    s.read_to_end(&mut raw).map_err(|e| e.to_string())?;
    let split = raw.windows(4).position(|w| w == b"\r\n\r\n").ok_or("the home's answer has no header")?;
    let head = String::from_utf8_lossy(&raw[..split]).to_string();
    let mut lines = head.split("\r\n");
    let status = lines
        .next()
        .and_then(|l| l.split(' ').nth(1))
        .and_then(|c| c.parse().ok())
        .ok_or("the home's answer has no status")?;
    let headers: Vec<(String, String)> = lines
        .filter_map(|l| l.split_once(':'))
        .map(|(k, v)| (k.trim().to_ascii_lowercase(), v.trim().to_string()))
        .collect();
    let mut body = raw[split + 4..].to_vec();
    if headers.iter().any(|(k, v)| k == "transfer-encoding" && v.eq_ignore_ascii_case("chunked")) {
        body = unchunk(&body);
    }
    Ok(Response { status, headers, body })
}

fn unchunk(mut b: &[u8]) -> Vec<u8> {
    let mut out = Vec::new();
    while let Some(end) = b.windows(2).position(|w| w == b"\r\n") {
        let size =
            usize::from_str_radix(String::from_utf8_lossy(&b[..end]).split(';').next().unwrap_or("0").trim(), 16)
                .unwrap_or(0);
        if size == 0 || b.len() < end + 2 + size {
            break;
        }
        out.extend_from_slice(&b[end + 2..end + 2 + size]);
        b = &b[(end + 4 + size).min(b.len())..];
    }
    out
}

/// The cookies a response sets, as name=value pairs.
fn cookies(r: &Response) -> Vec<(String, String)> {
    r.headers
        .iter()
        .filter(|(k, _)| k == "set-cookie")
        .filter_map(|(_, v)| {
            v.split(';').next()?.split_once('=').map(|(n, v)| (n.trim().to_string(), v.trim().to_string()))
        })
        .collect()
}

/// The multipart body of an install (the fields first, the file last, as the route reads it).
pub fn install_form(boundary: &str, fields: &[(&str, &str)], pkg: &[u8]) -> Vec<u8> {
    let mut b = Vec::new();
    for (k, v) in fields {
        b.extend_from_slice(
            format!("--{boundary}\r\nContent-Disposition: form-data; name=\"{k}\"\r\n\r\n{v}\r\n").as_bytes(),
        );
    }
    b.extend_from_slice(
        format!("--{boundary}\r\nContent-Disposition: form-data; name=\"file\"; filename=\"plugin.mkplugin\"\r\nContent-Type: application/octet-stream\r\n\r\n")
            .as_bytes(),
    );
    b.extend_from_slice(pkg);
    b.extend_from_slice(format!("\r\n--{boundary}--\r\n").as_bytes());
    b
}

fn password() -> Result<String, String> {
    if let Ok(p) = std::env::var("MKP_HOME_PASSWORD") {
        return Ok(p);
    }
    eprint!("The home admin's password (it is shown as you type; set MKP_HOME_PASSWORD to skip this): ");
    let mut line = String::new();
    std::io::stdin().lock().read_line(&mut line).map_err(|e| e.to_string())?;
    Ok(line.trim_end_matches(['\r', '\n']).to_string())
}

/// The plugin's English name, which an unreviewed install asks for.
fn english_name(dir: &Path, name_key: &str) -> String {
    std::fs::read(dir.join("i18n").join("en.json"))
        .ok()
        .and_then(|b| serde_json::from_slice::<serde_json::Value>(&b).ok())
        .and_then(|v| v.get(name_key).and_then(|n| n.as_str()).map(str::to_string))
        .unwrap_or_default()
}

pub fn run(dir: &Path, home: &str, user: &str) -> Result<(), String> {
    let m = crate::validate(dir)?;
    let name_key =
        m.get("strings").and_then(|s| s.get("name")).and_then(|n| n.as_str()).unwrap_or("plugin.name").to_string();
    let (addr, host) = parse_home(home)?;
    let tmp = std::env::temp_dir().join(format!("mkp-run-{}.mkplugin", std::process::id()));
    crate::pack::pack(dir, &tmp, None)?;
    let pkg = std::fs::read(&tmp).map_err(|e| e.to_string());
    let _ = std::fs::remove_file(&tmp);
    let pkg = pkg?;
    let pw = password()?;
    let login = serde_json::json!({"username": user, "password": pw}).to_string();
    let json = vec![("Content-Type".to_string(), "application/json".to_string())];
    let r = request(&addr, &host, "POST", "/api/auth/login", &json, login.as_bytes())?;
    if r.status != 200 {
        return Err(format!("signing in as {user}: {} {}", r.status, String::from_utf8_lossy(&r.body)));
    }
    let jar = cookies(&r);
    let cookie = jar.iter().map(|(n, v)| format!("{n}={v}")).collect::<Vec<_>>().join("; ");
    let csrf = jar.iter().find(|(n, _)| n == "mkt_csrf").map(|(_, v)| v.clone()).unwrap_or_default();
    let boundary = format!("mkp{}", std::process::id());
    let name = english_name(dir, &name_key);
    let body = install_form(&boundary, &[("confirm_name", &name), ("enable", "true"), ("development", "true")], &pkg);
    let headers = vec![
        ("Content-Type".to_string(), format!("multipart/form-data; boundary={boundary}")),
        ("Cookie".to_string(), cookie),
        ("X-Maktaba-CSRF".to_string(), csrf),
    ];
    let r = request(&addr, &host, "POST", "/api/plugins/install", &headers, &body)?;
    let answer: serde_json::Value = serde_json::from_slice(&r.body).unwrap_or_default();
    if r.status != 200 && r.status != 201 {
        let code = answer.get("code").and_then(|c| c.as_str()).unwrap_or("");
        let detail = answer.get("detail").and_then(|c| c.as_str()).unwrap_or("");
        return Err(format!("the home refused it: {} {code}: {detail}", r.status));
    }
    let res = &answer["result"];
    let verb = if res["updated"].as_bool().unwrap_or(false) { "updated on" } else { "installed on" };
    println!(
        "{} {} {verb} {home} (Development, turned on)",
        res["plugin_id"].as_str().unwrap_or(""),
        res["version"].as_str().unwrap_or("")
    );
    if let Some(e) = res["enable_error"].as_str().filter(|e| !e.is_empty()) {
        println!("not turned on: {e}");
    }
    Ok(())
}
