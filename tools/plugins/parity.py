"""``plugins-parity`` (Epic 54, story 54.26 §2, §6): each moved stage does exactly what it did.

Runs the parity tests: the self-tests of the harness (a toy stage with planted differences,
``pipeline/tests/plugins/firstparty``) and each first-party plugin's own
(``plugins/first-party/<name>/tests/test_parity*.py``), which run the plugin's stages on two
copies of one seeded database, in-process and through the plugin with the real plugin API,
and compare rows, files, results, fingerprints, enqueued jobs and job times.

    python3 tools/plugins/parity.py                       # every migrated stage
    python3 tools/plugins/parity.py --stages promo_links,clip_groups

Needs ``MAKTABA_TEST_PG_URL`` (a Postgres the tests may create databases on) and Go (the
plugin API's dev server is built from api/tools/plugin-api-dev). ``--report`` writes the job
times and plugin API calls per job as JSON (the CI keeps it as an artifact).
"""

from __future__ import annotations

import argparse
import os
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def targets() -> list[str]:
    out = [str(ROOT / "pipeline" / "tests" / "plugins" / "firstparty" / "test_parity_selftest.py")]
    first = ROOT / "plugins" / "first-party"
    if first.is_dir():
        out += [str(p) for p in sorted(first.glob("*/tests/test_parity*.py"))]
    return out


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--stages", default="", help="comma separated; empty: every migrated stage")
    ap.add_argument("--report", default="", help="write the numbers here (JSON)")
    args = ap.parse_args(argv)
    if not os.environ.get("MAKTABA_TEST_PG_URL"):
        print("parity: MAKTABA_TEST_PG_URL is not set", file=sys.stderr)  # noqa: T201
        return 2
    env = {**os.environ, "MAKTABA_PARITY_STAGES": args.stages, "MAKTABA_PARITY_REPORT": args.report}
    cmd = ["uv", "run", "--extra", "dev", "pytest", "-q", "-p", "no:cacheprovider", *targets()]
    return subprocess.run(cmd, cwd=ROOT / "pipeline", env=env, check=False).returncode


if __name__ == "__main__":
    sys.exit(main())
