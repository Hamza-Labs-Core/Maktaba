//! A plugin written with Rust std that reaches for the network and the disk. std on
//! wasm32-wasip2 imports wasi:sockets, wasi:filesystem and wasi:cli/stdin for these:
//! the audit refuses it (plugin_forbidden_import), and with the audit bypassed the
//! linker cannot instantiate it, because the host never links those interfaces.

wit_bindgen::generate!({ path: "../../../shared/plugins/wit", world: "plugin" });

struct P;

impl Guest for P {
    fn run(stage: String) -> Result<RunOutput, RunError> {
        let out = match stage.as_str() {
            "net" => std::net::TcpStream::connect("203.0.113.10:80").map(|_| ()).map_err(|e| e.to_string()),
            "dns" => {
                std::net::ToSocketAddrs::to_socket_addrs("example.invalid:80").map(|_| ()).map_err(|e| e.to_string())
            }
            "fs" => std::fs::read("/etc/passwd").map(|_| ()).map_err(|e| e.to_string()),
            _ => {
                let mut s = String::new();
                std::io::stdin().read_line(&mut s).map(|_| ()).map_err(|e| e.to_string())
            }
        };
        match out {
            Ok(()) => Ok(RunOutput { output_hash: "escaped".into(), summary: "{}".into() }),
            Err(e) => Err(RunError { code: "denied".into(), message: e }),
        }
    }
}

export!(P);
