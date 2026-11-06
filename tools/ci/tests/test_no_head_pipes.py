"""No shell script in tools/ and no workflow step pipes into a reader that leaves early.

They run under pipefail (`set -euo pipefail`; a workflow's `shell: bash` is
`bash -eo pipefail`). `head` leaves after its lines, so a producer that writes more than
a pipe holds gets SIGPIPE, and the pipe fails: the Linux app's end to end stopped there
with exit 2 and no message (`find | sort | head -n 1` over a venv's RECORD files). `sed -n
1,Np` prints the same lines and reads all its input.

`grep -q`, `grep -m N` and `sed …q` leave early too (#647: `curl … | grep -qi '<html'`
under pipefail fails with curl's exit 23 when the page is bigger than a pipe holds and
the match comes early). Feed them without a pipe instead: `grep -q x <<<"$out"`,
`grep -q x < <(cmd)` (pipefail does not see a process substitution), or
`[ -n "$(find … -print -quit)" ]`. Not `| grep -c … >/dev/null` or `| grep … >/dev/null`:
GNU grep stops at the first match when its output is /dev/null."""

from __future__ import annotations

import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]

HEAD_PIPE = re.compile(r"\|\s*head\b")
# a reader that leaves early, at the start of a line or after a pipe
_EARLY = (
    # an option before the pattern: -q, -qxF, -m1, -E -q, --quiet, --max-count=1
    r"(?:grep|egrep|fgrep)(?:\s+-[-\w]+)*?\s+(?:-[a-zA-Z]*[qm]\w*|--quiet|--silent|--max-count)"
    # a script that ends in q: sed 1q, sed '/x/q', sed -n '1p;q'
    r"|sed\b[^|;&]*?[\s'\"0-9/;$]q\d*['\"]?(?=\s|\)|$)"
)
EARLY_PIPE = re.compile(r"(?<!\|)\|(?!\|)\s*(?:" + _EARLY + ")")  # not ||
EARLY_START = re.compile(r"^\s*(?:" + _EARLY + ")")


def _scripts(root: Path) -> list[Path]:
    out = sorted(root.glob("tools/**/*.sh")) + sorted(
        root.glob(".github/workflows/*.y*ml")
    )
    return [p for p in out if p.is_file()]


def _pipes(root: Path, early: bool) -> list[str]:
    found = []
    for p in _scripts(root):
        rel = p.relative_to(root).as_posix()
        prev = ""
        for n, line in enumerate(
            p.read_text(encoding="utf-8", errors="replace").splitlines(), 1
        ):
            if line.lstrip().startswith("#"):
                continue
            if early:
                hit = EARLY_PIPE.search(line) or (
                    prev.rstrip().endswith("|") and EARLY_START.search(line)
                )
            else:
                hit = HEAD_PIPE.search(line) or (
                    prev.rstrip().endswith("|") and line.lstrip().startswith("head")
                )
            if hit:
                found.append(f"{rel}:{n}: {line.strip()}")
            prev = line
    return found


def head_pipes(root: Path) -> list[str]:
    return _pipes(root, early=False)


def early_exit_pipes(root: Path) -> list[str]:
    return _pipes(root, early=True)


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


def test_no_script_pipes_into_an_early_exit_reader() -> None:
    assert early_exit_pipes(ROOT) == []


def test_early_exit_readers_are_found(tmp_path: Path) -> None:
    f = tmp_path / "tools" / "x.sh"
    f.parent.mkdir(parents=True)
    f.write_text(
        "set -euo pipefail\n"
        "curl -fsS http://x/ | grep -qi '<html' || die\n"  # 2
        "out=\"$(cmd | grep -m1 '^vector ')\"\n"  # 3
        "cmd | sed 1q\n"  # 4
        "cmd | grep --quiet x\n"  # 5
        "otool -L f | awk '{print $1}' |\n"
        "    grep -vE '^/usr' | grep -q .\n"  # 7
        "find . |\n"
        "    grep -q x\n"  # 9
        "# cmd | grep -q x: a comment\n"
        'grep -q x <<<"$out"\n'
        "grep -q x < <(cmd)\n"
        "cmd | grep -v x | sed -n 1,2p\n"
        "cmd | grep -c x\n"
        "cmd | sed -n 's/^install //p'\n"
        "cmd | sed 's/q/x/'\n"
        'n="$(cmd | grep -c "^a\\.$(uname -m) ")"\n'
        "grep -q a f || grep -q b < <(cmd)\n"
        "cmd | sed -n '1p;q'\n"  # 19
    )
    assert [line.split(":")[1] for line in early_exit_pipes(tmp_path)] == [
        "2",
        "3",
        "4",
        "5",
        "7",
        "9",
        "19",
    ]


def test_grep_q_fails_the_pipe_and_the_safe_forms_do_not() -> None:
    def run(check: str) -> int:
        return subprocess.run(
            ["bash", "-c", f"set -euo pipefail; {check}"], check=False
        ).returncode

    assert run("seq 1 200000 | grep -q '^1$'") != 0
    assert run("grep -q '^1$' < <(seq 1 200000)") == 0
    assert run('out="$(seq 1 200000)"; grep -q \'^1$\' <<<"$out"') == 0
