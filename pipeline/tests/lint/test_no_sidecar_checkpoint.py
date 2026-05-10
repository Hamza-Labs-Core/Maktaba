"""Architectural smoke test for Story 6.10's invariant.

The DB is the only checkpoint for resume. Sidecar JSON or partial
files create a second source of truth for resume offsets and have
caused production incidents in prior systems
(architecture §7.6 / §7.9). This test fails the build if any code
path implies a sidecar.

Allowlist: a line containing ``# resume-invariant-ok: <reason>`` is
exempt. Real production code should never need this exception; tests
and documentation that *describe* the rejected pattern are the
expected callers.
"""

from __future__ import annotations

import pathlib
import re

import pytest

PIPELINE_SRC = pathlib.Path(__file__).resolve().parents[2] / "src"
PIPELINE_TESTS = pathlib.Path(__file__).resolve().parents[2] / "tests"

# The patterns that flag a sidecar checkpoint. Conservative — file
# names and method names matching these substrings always trigger.
PATTERNS = [
    re.compile(r"\bpartial\.json\b"),  # resume-invariant-ok: lint pattern
    re.compile(r"\bcheckpoint\.json\b"),  # resume-invariant-ok: lint pattern
    re.compile(r"\.partial\b"),  # resume-invariant-ok: lint pattern
    re.compile(r"\b_resume_offset\b"),  # resume-invariant-ok: lint pattern
    re.compile(r"\b_resume_position\b"),  # resume-invariant-ok: lint pattern
    re.compile(r"\bsidecar_checkpoint\b"),  # resume-invariant-ok: lint pattern
]

ALLOWLIST_MARKER = "resume-invariant-ok"


@pytest.mark.parametrize("root", [PIPELINE_SRC, PIPELINE_TESTS])
def test_no_sidecar_checkpoint_strings(root: pathlib.Path) -> None:
    hits: list[str] = []
    for path in root.rglob("*.py"):
        if "__pycache__" in path.parts or ".venv" in path.parts:
            continue
        try:
            text = path.read_text()
        except UnicodeDecodeError:
            continue
        for i, line in enumerate(text.splitlines(), 1):
            if ALLOWLIST_MARKER in line:
                continue
            for pat in PATTERNS:
                if pat.search(line):
                    hits.append(f"{path.relative_to(root.parent)}:{i}: {line.strip()}")
                    break

    assert not hits, (
        "Sidecar-checkpoint patterns found. processing_jobs."
        "last_segment_end_sec is the canonical resume offset (Story "
        f"6.10). Mark intentional references with `# {ALLOWLIST_MARKER}: "
        "<reason>`.\n\n" + "\n".join(hits)
    )
