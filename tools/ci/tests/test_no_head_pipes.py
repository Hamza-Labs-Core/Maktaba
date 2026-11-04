"""No shell script in tools/ and no workflow step pipes into `head`.

They run under pipefail (`set -euo pipefail`; a workflow's `shell: bash` is
`bash -eo pipefail`). `head` leaves after its lines, so a producer that writes more than
a pipe holds gets SIGPIPE, and the pipe fails: the Linux app's end to end stopped there
with exit 2 and no message (`find | sort | head -n 1` over a venv's RECORD files). `sed -n
1,Np` prints the same lines and reads all its input."""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]

HEAD_PIPE = re.compile(r"\|\s*head\b")

# Edited by another change when this rule came in (#647: replace its head, then drop it).
KNOWN = {".github/workflows/testflight.yml"}


def _scripts(root: Path) -> list[Path]:
    out = sorted(root.glob("tools/**/*.sh")) + sorted(
        root.glob(".github/workflows/*.y*ml")
    )
    return [p for p in out if p.is_file()]


def head_pipes(root: Path) -> list[str]:
    found = []
    for p in _scripts(root):
        rel = p.relative_to(root).as_posix()
        if rel in KNOWN:
            continue
        for n, line in enumerate(
            p.read_text(encoding="utf-8", errors="replace").splitlines(), 1
        ):
            if line.lstrip().startswith("#"):
                continue
            if HEAD_PIPE.search(line):
                found.append(f"{rel}:{n}: {line.strip()}")
    return found


def test_no_script_pipes_into_head() -> None:
    assert head_pipes(ROOT) == []


def test_the_e2e_line_that_ended_the_run_is_found(tmp_path: Path) -> None:
    f = tmp_path / "tools" / "release" / "linux-app" / "e2e.sh"
    f.parent.mkdir(parents=True)
    f.write_text(
        "set -euo pipefail\n"
        "# find | head under pipefail: a comment, not a pipe\n"
        'record="$(find "$DATA/venv/lib" -print 2>/dev/null |\n'
        '    LC_ALL=C sort | head -n 1)"\n'
    )
    assert head_pipes(tmp_path) == [
        'tools/release/linux-app/e2e.sh:4: LC_ALL=C sort | head -n 1)"'
    ]


def test_head_fails_the_pipe_and_sed_does_not() -> None:
    # more than a pipe holds, as the venv's RECORD list is
    def run(take: str) -> int:
        return subprocess.run(
            [
                "bash",
                "-c",
                f'set -euo pipefail; x="$(seq 1 200000 | {take})"; [ "$x" = 1 ]',
            ],
            check=False,
        ).returncode

    assert run("head -n 1") != 0
    assert run("sed -n 1p") == 0
