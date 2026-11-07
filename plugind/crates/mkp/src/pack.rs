//! `mkp pack`: a package folder as a .mkplugin (story 54.01 §1): a zstd tar whose head is
//! manifest.toml then files.json (every file, manifest.toml included, with its sha256 and
//! size, sorted by path), then the files. The same bytes as the home's Go writer
//! (componentpack.BuildPlugin) for the same files.

use std::io::Write;
use std::path::Path;

use sha2::{Digest, Sha256};

/// Every regular file of dir by its path inside it ('/'-separated), hidden ones left out.
pub fn files_of(dir: &Path) -> Result<Vec<(String, Vec<u8>)>, String> {
    fn walk(root: &Path, dir: &Path, out: &mut Vec<(String, Vec<u8>)>) -> Result<(), String> {
        let mut entries: Vec<_> =
            std::fs::read_dir(dir).map_err(|e| format!("{}: {e}", dir.display()))?.flatten().collect();
        entries.sort_by_key(|e| e.file_name());
        for e in entries {
            let name = e.file_name().to_string_lossy().into_owned();
            if name.starts_with('.') {
                continue;
            }
            let ft = e.file_type().map_err(|e| e.to_string())?;
            let p = e.path();
            if ft.is_symlink() {
                return Err(format!("{}: a link (packages hold files only)", p.display()));
            }
            if ft.is_dir() {
                walk(root, &p, out)?;
            } else if ft.is_file() {
                let rel = p.strip_prefix(root).unwrap().to_string_lossy().replace('\\', "/");
                out.push((rel, std::fs::read(&p).map_err(|e| e.to_string())?));
            }
        }
        Ok(())
    }
    let mut out = Vec::new();
    walk(dir, dir, &mut out)?;
    out.sort_by(|a, b| a.0.cmp(&b.0));
    Ok(out)
}

/// files.json's text for files.
pub fn files_json(files: &[(String, Vec<u8>)]) -> String {
    let list: Vec<serde_json::Value> = files
        .iter()
        .map(|(p, b)| serde_json::json!({"path": p, "sha256": hex::encode(Sha256::digest(b)), "size": b.len()}))
        .collect();
    serde_json::to_string(&list).unwrap()
}

/// Writes the package of files to w; answers the sha256 of files.json.
pub fn write(files: &[(String, Vec<u8>)], w: impl Write) -> Result<String, String> {
    let manifest = files.iter().find(|(p, _)| p == "manifest.toml").ok_or("no manifest.toml")?;
    let fj = files_json(files);
    let enc = zstd::stream::write::Encoder::new(w, 3).map_err(|e| e.to_string())?;
    let mut tar = tar::Builder::new(enc);
    let mut add = |name: &str, body: &[u8]| -> Result<(), String> {
        let mut h = tar::Header::new_ustar();
        h.set_size(body.len() as u64);
        h.set_mode(0o644);
        h.set_mtime(0);
        h.set_entry_type(tar::EntryType::Regular);
        tar.append_data(&mut h, name, body).map_err(|e| format!("{name}: {e}"))
    };
    add("manifest.toml", &manifest.1)?;
    add("files.json", fj.as_bytes())?;
    for (p, b) in files {
        if p != "manifest.toml" {
            add(p, b)?;
        }
    }
    let enc = tar.into_inner().map_err(|e| e.to_string())?;
    enc.finish().map_err(|e| e.to_string())?;
    Ok(hex::encode(Sha256::digest(fj.as_bytes())))
}

pub fn pack(dir: &Path, out: &Path) -> Result<(), String> {
    crate::validate(dir)?;
    let files = files_of(dir)?;
    if files.len() > 500 {
        return Err(format!("{} files, at most 500", files.len()));
    }
    let f = std::fs::File::create(out).map_err(|e| format!("{}: {e}", out.display()))?;
    let sum = write(&files, std::io::BufWriter::new(f))?;
    let size = std::fs::metadata(out).map(|m| m.len()).unwrap_or(0);
    if size > 50 << 20 {
        let _ = std::fs::remove_file(out);
        return Err(format!("the package is {size} bytes, at most 50 MB"));
    }
    println!("{} ({} files, files.json sha256 {sum})", out.display(), files.len());
    Ok(())
}
