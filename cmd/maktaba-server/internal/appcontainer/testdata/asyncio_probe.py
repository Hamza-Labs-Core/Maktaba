"""Inside a runner group's AppContainer (story 54.23, CI's windows-2022 job only): a
stage's captured tool start (asyncio.create_subprocess_exec with stdout and stderr PIPE),
with asyncio's own pipes and with the runner's anonymous ones (winproc). Writes its findings
as JSON to the path given; never raises past a finding."""

from __future__ import annotations

import asyncio
import importlib.util
import json
import subprocess
import sys
import time

out_path, winproc_path = sys.argv[1:3]
found: dict[str, object] = {}
CHILD = [
    sys.executable,
    "-c",
    "import sys; sys.stdout.write('o'*(4<<20)); sys.stderr.write('done'); sys.exit(7)",
]


def attempt(name: str, fn) -> None:  # type: ignore[no-untyped-def]
    try:
        found[name] = ["ok", fn()]
    except PermissionError as exc:
        found[name] = ["refused", str(exc)]
    except Exception as exc:  # noqa: BLE001
        found[name] = ["error", f"{type(exc).__name__}: {exc}"]


async def captured() -> dict[str, object]:
    proc = await asyncio.create_subprocess_exec(
        *CHILD, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.PIPE
    )
    out, err = await asyncio.wait_for(proc.communicate(), 60)
    return {
        "code": proc.returncode,
        "stdout_bytes": len(out or b""),
        "stderr": (err or b"").decode(),
    }


async def cancelled() -> dict[str, object]:
    proc = await asyncio.create_subprocess_exec(
        sys.executable, "-c", "import time; time.sleep(60)", stdout=subprocess.PIPE
    )
    start = time.perf_counter()
    try:
        await asyncio.wait_for(proc.communicate(), 0.5)
    except TimeoutError:
        pass
    code = await asyncio.wait_for(proc.wait(), 10)
    return {"code": code, "seconds": time.perf_counter() - start}


# asyncio's own: named pipes, which an AppContainer may not create
attempt("asyncio_pipes", lambda: asyncio.run(captured()))

spec = importlib.util.spec_from_file_location("winproc", winproc_path)
assert spec and spec.loader
winproc = importlib.util.module_from_spec(spec)
spec.loader.exec_module(winproc)
winproc.install()
attempt("anonymous_pipes", lambda: asyncio.run(captured()))
attempt("cancelled", lambda: asyncio.run(cancelled()))

with open(out_path, "w", encoding="utf-8") as f:
    json.dump(found, f, indent=1)
