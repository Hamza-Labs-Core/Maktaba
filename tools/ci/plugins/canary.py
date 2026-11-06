"""The canary of the plugins-sandbox CI job (Epic 54, story 54.03 §5).

Listens on 127.0.0.1, ::1 and the runner's LAN address, TCP and UDP, on one port
(default 47001), and counts every connection and packet. UDP packets are echoed, so a
probe counts as ALLOWED only when it heard back. The count is written to --count after
every event, so the job reads it between the sandboxed run and the positive control.

    python3 canary.py --port 47001 --count canary.count --lan-file lan.txt &

CI only: it runs beside the escape probes, never on a developer's machine.
"""

from __future__ import annotations

import argparse
import socket
import threading
from pathlib import Path


def lan_address() -> str:
    """The address the default route leaves from (no packet is sent)."""
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect(("192.0.2.1", 9))
        return str(s.getsockname()[0])
    except OSError:
        return ""
    finally:
        s.close()


class Counter:
    def __init__(self, path: Path) -> None:
        self.n = 0
        self.path = path
        self.lock = threading.Lock()
        self.path.write_text("0\n")

    def hit(self, what: str) -> None:
        with self.lock:
            self.n += 1
            self.path.write_text(f"{self.n}\n")
        print(f"canary: {what}", flush=True)


def serve_tcp(fam: int, addr: str, port: int, c: Counter) -> None:
    s = socket.socket(fam, socket.SOCK_STREAM)
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((addr, port))
    s.listen(16)
    while True:
        conn, peer = s.accept()
        c.hit(f"tcp {addr} from {peer}")
        conn.close()


def serve_udp(fam: int, addr: str, port: int, c: Counter) -> None:
    s = socket.socket(fam, socket.SOCK_DGRAM)
    s.bind((addr, port))
    while True:
        data, peer = s.recvfrom(2048)
        c.hit(f"udp {addr} from {peer}")
        s.sendto(data, peer)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=47001)
    ap.add_argument("--count", required=True)
    ap.add_argument("--lan-file", required=True)
    a = ap.parse_args()
    c = Counter(Path(a.count))
    lan = lan_address()
    Path(a.lan_file).write_text(lan + "\n")
    addrs = [(socket.AF_INET, "127.0.0.1"), (socket.AF_INET6, "::1")]
    if lan:
        addrs.append((socket.AF_INET, lan))
    threads = []
    for fam, addr in addrs:
        for fn in (serve_tcp, serve_udp):
            t = threading.Thread(target=fn, args=(fam, addr, a.port, c), daemon=True)
            t.start()
            threads.append(t)
    print(f"canary: listening on {[x for _, x in addrs]} port {a.port}", flush=True)
    for t in threads:
        t.join()


if __name__ == "__main__":
    main()
