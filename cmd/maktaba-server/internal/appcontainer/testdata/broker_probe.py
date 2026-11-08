"""Inside a runner group's AppContainer (story 54.23, CI's windows-2022 job only): what the
runner reaches on a share with and without its launcher's file broker. Writes its findings
as JSON to the path given; never raises past a finding."""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import sys
import time

out_path, winfiles_path, lib, other_share = sys.argv[1:5]
video = lib + "\\show\\ep1.mkv"
found: dict[str, object] = {}


def attempt(name: str, fn) -> None:  # type: ignore[no-untyped-def]
    try:
        found[name] = ["ok", fn()]
    except PermissionError as exc:
        found[name] = ["refused", str(exc)]
    except FileNotFoundError as exc:
        found[name] = ["missing", str(exc)]
    except Exception as exc:  # noqa: BLE001
        found[name] = ["error", f"{type(exc).__name__}: {exc}"]


def read_all(path: str) -> int:
    with open(path, "rb") as f:
        n = 0
        while chunk := f.read(8 << 20):
            n += len(chunk)
    return n


# without the broker: an AppContainer reaches no share (the jobs went to the worker)
attempt("direct_read", lambda: read_all(video))

spec = importlib.util.spec_from_file_location("winfiles", winfiles_path)
assert spec and spec.loader
winfiles = importlib.util.module_from_spec(spec)
spec.loader.exec_module(winfiles)
found["installed"] = winfiles.install_from_env()


def timed_read() -> dict[str, float]:
    start = time.perf_counter()
    n = read_all(video)
    secs = time.perf_counter() - start
    return {"bytes": n, "seconds": secs, "mb_per_s": n / (1 << 20) / max(secs, 1e-9)}


attempt("read", timed_read)
attempt("exists", lambda: os.path.exists(video))
attempt("list", lambda: sorted(os.listdir(lib + "\\show")))
attempt("read_any_case", lambda: read_all(video.upper()))


def write_subtitle() -> str:
    sub = lib + "\\show\\ep1.en.srt"
    with open(sub + ".maktaba-tmp", "wb") as f:
        f.write(b"1\n00:00:00,000 --> 00:00:01,000\nhello\n")
    os.replace(sub + ".maktaba-tmp", sub)
    with open(sub, "rb") as f:
        return f.read().decode()


attempt("write_subtitle", write_subtitle)


def write(path: str) -> None:
    with open(path, "wb") as f:
        f.write(b"x")


attempt("write_other_name", lambda: write(lib + "\\show\\notes.txt"))
attempt("write_video", lambda: write(video))
attempt("remove_video", lambda: os.remove(video))
attempt("traversal", lambda: read_all(lib + "\\show\\..\\..\\outside.txt"))
attempt("junction_escape", lambda: read_all(lib + "\\escape\\secret.txt"))
attempt("short_name", lambda: read_all(lib + "\\show\\EP1~1.MKV"))
attempt("other_share", lambda: read_all(other_share + "\\secret.txt"))
attempt("stream", lambda: read_all(video + ":hidden"))
attempt(
    "tool_on_share",
    lambda: subprocess.run([sys.executable, "-c", "pass", video], check=False).returncode,
)

# a process of the same AppContainer that is not the runner (a tool) is not served
child = (
    "import os\n"
    "try:\n"
    "    f = open(os.environ['MAKTABA_RUNNER_FILES_PIPE'], 'r+b', buffering=0)\n"
    "    f.write(b'{\"op\":\"list\",\"path\":\"x\"}\\n')\n"
    "    print('answered' if f.read(4096) else 'closed')\n"
    "except OSError as exc:\n"
    "    print('closed', exc)\n"
)
attempt(
    "child_on_pipe",
    lambda: subprocess.run(
        [sys.executable, "-c", child], capture_output=True, text=True, check=False
    ).stdout.strip(),
)

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(found, f, indent=1)
