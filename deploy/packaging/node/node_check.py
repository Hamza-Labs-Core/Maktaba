"""Has this machine joined a Maktaba home? (deploy/packaging/node, Epic 31 design section 7)

Run with the node's own Python (the venv the installers build):

    <venv>/bin/python node_check.py

It reads the node config through ``maktaba_pipeline.cluster.config``, so the per-OS path
and the ``MAKTABA_NODE_CONFIG`` override are exactly the ones the worker uses.

Exit 0 when the config holds a joined machine (it has the home's database URL), and 78
(EX_CONFIG) when it does not or cannot be read. The installers call it to decide whether
to run ``maktaba-pipeline join``; the services call it before every start, because
``maktaba-pipeline run`` without a joined config would quietly fall back to an
in-memory database instead of working for the home.
"""

from __future__ import annotations

import sys

EX_CONFIG = 78


def main() -> int:
    try:
        from maktaba_pipeline.cluster.config import config_path, load
    except ImportError as exc:  # the venv is missing or broken
        sys.stderr.write(f"maktaba-node: the pipeline is not installed in this Python: {exc}\n")
        return EX_CONFIG
    path = config_path()
    try:
        cfg = load(path)
    except (OSError, ValueError, KeyError) as exc:
        sys.stderr.write(f"maktaba-node: cannot read the node config {path}: {exc}\n")
        return EX_CONFIG
    if cfg is None or not cfg.joined:
        sys.stderr.write(
            f"maktaba-node: this machine has not joined a Maktaba home (no joined config at "
            f"{path}). Run: maktaba-pipeline join\n"
        )
        return EX_CONFIG
    home = cfg.home.name or cfg.home.url or "the home"
    sys.stdout.write(f"maktaba-node: joined {home} as {cfg.name or cfg.node8} ({path})\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
