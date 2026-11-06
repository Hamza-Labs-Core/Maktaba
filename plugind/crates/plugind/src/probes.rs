//! Escape probes (story 54.03 §5.2), built only with `--features escape-probes`, never
//! shipped. After the real sandbox is applied, native code (stronger than any plugin)
//! tries to get out. Each probe prints `PROBE <name> DENIED|ALLOWED <detail>`.
//!
//! Run only in CI (`plugins-sandbox`), never on a developer's or the owner's machine. The
//! canary listener the job runs on 127.0.0.1, ::1 and the LAN address counts every
//! connection; the public literal is TEST-NET-3 (RFC 5737), routed nowhere, so even a
//! probe that fails open reaches no one.
//!
//! Inputs (environment): PROBE_PORT (default 47391), PROBE_LAN (the runner's LAN address),
//! PROBE_SECRET (a planted fake key), PROBE_STATE (a planted file in the data folder),
//! PROBE_WRITE (a path outside the run folder).

use std::io::{Read, Write};
use std::net::{SocketAddr, TcpListener, TcpStream, ToSocketAddrs, UdpSocket};
use std::time::Duration;

/// One probe's answer.
pub struct Probe {
    pub name: String,
    /// "network", "file" or "process": the positive control expects network and file
    /// probes ALLOWED without the sandbox (process and raw probes need privileges or
    /// differ per OS and are not part of it)
    pub kind: &'static str,
    pub allowed: bool,
    pub detail: String,
}

fn probe(name: &str, kind: &'static str, r: std::io::Result<String>) -> Probe {
    match r {
        Ok(d) => Probe { name: name.into(), kind, allowed: true, detail: d },
        Err(e) => Probe { name: name.into(), kind, allowed: false, detail: format!("{} ({:?})", e, e.raw_os_error()) },
    }
}

fn tcp(addr: SocketAddr) -> std::io::Result<String> {
    let mut s = TcpStream::connect_timeout(&addr, Duration::from_secs(2))?;
    let _ = s.write_all(b"probe\n");
    Ok(format!("connected to {addr}"))
}

/// ALLOWED only when the canary's echo comes back: a send a firewall drops silently is
/// not a way out.
fn udp(addr: SocketAddr) -> std::io::Result<String> {
    let bind: SocketAddr = if addr.is_ipv6() { "[::]:0".parse().unwrap() } else { "0.0.0.0:0".parse().unwrap() };
    let s = UdpSocket::bind(bind)?;
    s.set_read_timeout(Some(Duration::from_secs(2)))?;
    s.send_to(b"probe\n", addr)?;
    let mut b = [0u8; 64];
    let (n, from) = s.recv_from(&mut b)?;
    Ok(format!("{n} bytes echoed by {from}"))
}

/// ALLOWED only when a port it binds can be reached: binding is local, a listener no one
/// can connect to is not a way in.
fn bind() -> std::io::Result<String> {
    let l = TcpListener::bind("127.0.0.1:0")?;
    let addr = l.local_addr()?;
    let _c = TcpStream::connect_timeout(&addr, Duration::from_secs(2))?;
    let (_s, peer) = l.accept()?;
    Ok(format!("bound {addr}, reached from {peer}"))
}

fn read(path: &str) -> std::io::Result<String> {
    let mut f = std::fs::File::open(path)?;
    let mut b = [0u8; 16];
    let n = f.read(&mut b)?;
    Ok(format!("read {n} bytes"))
}

pub fn run() -> Vec<Probe> {
    let port: u16 = std::env::var("PROBE_PORT").ok().and_then(|p| p.parse().ok()).unwrap_or(47391);
    let mut out = Vec::new();
    let mut targets: Vec<(String, SocketAddr)> = vec![
        ("loopback4".into(), SocketAddr::from(([127, 0, 0, 1], port))),
        ("loopback6".into(), format!("[::1]:{port}").parse().unwrap()),
    ];
    if let Some(lan) = std::env::var("PROBE_LAN").ok().filter(|s| !s.is_empty()) {
        if let Ok(ip) = lan.parse::<std::net::IpAddr>() {
            targets.push(("lan".into(), SocketAddr::new(ip, port)));
        }
    }
    for (n, a) in &targets {
        out.push(probe(&format!("tcp_{n}"), "network", tcp(*a)));
        out.push(probe(&format!("udp_{n}"), "network", udp(*a)));
    }
    // TEST-NET-3: never routed; a connect can only fail, but it must fail by refusal
    out.push(probe("tcp_public", "public", tcp("203.0.113.10:80".parse().unwrap())));
    // a name: on Unix the resolver needs files or a socket the sandbox refuses; Windows
    // answers localhost without the network, so there a name it would have to look up
    #[cfg(unix)]
    out.push(probe("dns", "network", ("localhost", 80).to_socket_addrs().map(|a| format!("{} addresses", a.count()))));
    #[cfg(windows)]
    out.push(probe(
        "dns",
        "dns",
        ("maktaba-probe.invalid", 80).to_socket_addrs().map(|a| format!("{} addresses", a.count())),
    ));
    // Windows lets a process in an AppContainer bind loopback and reach its own listener
    // (both ends are inside the container); no one outside can connect in, since the
    // container has no inbound capability. There the probe is "local": reported, and not
    // a way out. Everywhere else the bind itself is refused.
    #[cfg(windows)]
    out.push(probe("bind", "local", bind()));
    #[cfg(not(windows))]
    out.push(probe("bind", "network", bind()));
    #[cfg(target_os = "linux")]
    {
        // SAFETY: socket() with constants; closed when it succeeds
        let fd = unsafe { libc::socket(libc::AF_NETLINK, libc::SOCK_RAW, 0) };
        out.push(probe(
            "netlink",
            "network",
            if fd >= 0 {
                unsafe { libc::close(fd) };
                Ok("netlink socket".into())
            } else {
                Err(std::io::Error::last_os_error())
            },
        ));
        // SAFETY: as above
        let fd = unsafe { libc::socket(libc::AF_PACKET, libc::SOCK_RAW, 0) };
        out.push(probe(
            "packet",
            "raw",
            if fd >= 0 {
                unsafe { libc::close(fd) };
                Ok("packet socket".into())
            } else {
                Err(std::io::Error::last_os_error())
            },
        ));
    }
    #[cfg(unix)]
    {
        // SAFETY: as above
        let fd = unsafe { libc::socket(libc::AF_INET, libc::SOCK_RAW, libc::IPPROTO_ICMP) };
        out.push(probe(
            "raw",
            "raw",
            if fd >= 0 {
                unsafe { libc::close(fd) };
                Ok("raw socket".into())
            } else {
                Err(std::io::Error::last_os_error())
            },
        ));
        out.push(probe("read_passwd", "file", read("/etc/passwd")));
    }
    if let Ok(p) = std::env::var("PROBE_SECRET") {
        out.push(probe("read_secret", "file", read(&p)));
    }
    if let Ok(p) = std::env::var("PROBE_STATE") {
        out.push(probe("read_state", "file", read(&p)));
    }
    if let Ok(p) = std::env::var("PROBE_WRITE") {
        out.push(probe("write_outside", "file", std::fs::write(&p, b"escaped").map(|_| format!("wrote {p}"))));
    }
    #[cfg(unix)]
    let spawn = std::process::Command::new("/bin/sh").arg("-c").arg("exit 0").status();
    #[cfg(windows)]
    let spawn = std::process::Command::new("cmd.exe").arg("/c").arg("exit 0").status();
    out.push(probe("spawn", "process", spawn.map(|s| format!("{s}"))));
    out
}

pub fn print(probes: &[Probe]) {
    for p in probes {
        println!(
            "PROBE {} {} {} {}",
            p.name,
            p.kind,
            if p.allowed { "ALLOWED" } else { "DENIED" },
            p.detail.replace('\n', " ")
        );
    }
}
