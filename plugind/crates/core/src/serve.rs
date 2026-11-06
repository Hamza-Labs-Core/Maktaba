//! The dispatch socket's server (story 54.02 §2): one thread per connection, one request
//! per connection. A `run` streams `progress` frames and ends with its `result`.

use std::io::{BufReader, BufWriter};
use std::sync::mpsc;
use std::sync::Arc;

use crate::engine::Host;
use crate::protocol::{b64, read_frame, write_frame, Request, Response};
use crate::uds::{UnixListener, UnixStream};

/// Serves until the listener fails.
pub fn serve(host: Arc<Host>, listener: UnixListener) -> std::io::Result<()> {
    for conn in listener.incoming() {
        let Ok(conn) = conn else { continue };
        let host = host.clone();
        std::thread::Builder::new().name("dispatch".into()).spawn(move || {
            let _ = handle(&host, conn);
        })?;
    }
    Ok(())
}

pub fn handle(host: &Host, conn: UnixStream) -> std::io::Result<()> {
    let mut r = BufReader::new(conn.try_clone()?);
    let mut w = BufWriter::new(conn);
    let req: Request = match read_frame(&mut r) {
        Ok(r) => r,
        Err(e) => {
            return write_frame(&mut w, &Response::Error { error_code: "bad_request".into(), detail: e.to_string() });
        }
    };
    match req {
        Request::Health => write_frame(&mut w, &Response::Health(host.health())),
        Request::Cancel { invocation_id } => write_frame(&mut w, &Response::Ack { ok: host.cancel(&invocation_id) }),
        Request::Unload { plugin_id, version } => {
            host.unload(&plugin_id, &version);
            write_frame(&mut w, &Response::Ack { ok: true })
        }
        Request::Preload { plugin_id, version, package_path } => {
            let r = host.preload(&plugin_id, &version, &package_path);
            let (ok, error_code, detail) = match r {
                Ok(()) => (true, String::new(), String::new()),
                Err(e) => (false, e.code.to_string(), e.detail),
            };
            write_frame(&mut w, &Response::Preloaded { plugin_id, version, ok, error_code, detail })
        }
        Request::Run(run) => {
            let (tx, rx) = mpsc::channel::<Response>();
            let id = run.invocation_id.clone();
            let progress_tx = tx.clone();
            let progress = Box::new(move |pct: Option<u8>, cp: Option<&[u8]>| {
                let _ = progress_tx.send(Response::Progress {
                    invocation_id: id.clone(),
                    pct: pct.unwrap_or(0),
                    checkpoint: cp.map(b64),
                });
            });
            let writer = std::thread::spawn(move || -> std::io::Result<()> {
                for msg in rx {
                    write_frame(&mut w, &msg)?;
                }
                Ok(())
            });
            let result = host.run(&run, progress);
            let _ = tx.send(Response::Result(result));
            drop(tx);
            writer.join().unwrap_or(Ok(()))
        }
    }
}
