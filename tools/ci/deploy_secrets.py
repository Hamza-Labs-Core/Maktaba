"""Both cloud deploy paths hand the api every secret it reads.

The cloud has two deploy paths: the single VPS (deploy/cloud-relay, Docker Compose) and the
Phase 1 pair (deploy/cloud-ha, systemd), each its own step in .github/workflows/relay.yml.
A secret wired into one path only leaves the other's api without it, silently: the feature
it turns on stays off there (story 53.16's symbol uploads stayed 503 symbols_off on the
single VPS that way). This guard checks:

- every token file the api reads (a "MAKTABA_CLOUD_*_FILE" name in cloud/cmd/maktaba-cloud)
  is named in each path's files, unless EXEMPT says why that path has none;
- every repository secret either deploy step is given is given to the other too, unless
  PATH_ONLY says why it belongs to one path (its hosts, its own infrastructure).

Run: python tools/ci/deploy_secrets.py (exit 1 with each gap named).
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[2]
API_DIR = "cloud/cmd/maktaba-cloud"
WORKFLOW = ".github/workflows/relay.yml"
PATHS = {"single VPS": "deploy/cloud-relay", "Phase 1 pair": "deploy/cloud-ha"}
STEPS = {"single VPS": "Deploy (the single VPS)", "Phase 1 pair": "Deploy (the Phase 1 pair)"}

# a token file a path has none of, with why
EXEMPT: dict[tuple[str, str], str] = {
    ("MAKTABA_CLOUD_LOGS_TOKEN_FILE", "single VPS"): (
        "the log store (W, Epic 53) runs beside the pair only; the single VPS keeps its logs "
        "on the box"
    ),
}

# a repository secret only one deploy step takes, with why
PATH_ONLY: dict[str, str] = {
    "RELAY_SSH_HOST": "the single VPS's host",
    "RELAY_HOST_A": "the pair's first node",
    "RELAY_HOST_B": "the pair's second node",
    "CLOUDFLARE_TUNNEL_TOKEN": "the Tunnel fronts the pair (story 50.31); the single VPS runs Caddy",
}

SECRET_RE = re.compile(r"\$\{\{\s*secrets\.([A-Z0-9_]+)")
FILE_ENV_RE = re.compile(r'"(MAKTABA_CLOUD_[A-Z0-9_]+_FILE)"')


def api_token_files(root: Path = ROOT) -> set[str]:
    """The token-file names the api reads."""
    out: set[str] = set()
    for f in sorted((root / API_DIR).glob("*.go")):
        if not f.name.endswith("_test.go"):
            out |= set(FILE_ENV_RE.findall(f.read_text()))
    return out


def path_text(root: Path, rel: str) -> str:
    return "\n".join(f.read_text(errors="replace") for f in sorted((root / rel).rglob("*")) if f.is_file())


def step_secrets(root: Path = ROOT) -> dict[str, set[str]]:
    """Each deploy step's repository secrets, by path."""
    wf = yaml.safe_load((root / WORKFLOW).read_text())
    found: dict[str, set[str]] = {}
    for job in wf.get("jobs", {}).values():
        for step in job.get("steps", []) or []:
            for path, name in STEPS.items():
                if step.get("name") == name:
                    env = step.get("env", {}) or {}
                    found[path] = {m for v in env.values() for m in SECRET_RE.findall(str(v))}
    return found


def gaps(root: Path = ROOT) -> list[str]:
    out: list[str] = []
    texts = {p: path_text(root, rel) for p, rel in PATHS.items()}
    for name in sorted(api_token_files(root)):
        for path, text in texts.items():
            if name not in text and (name, path) not in EXEMPT:
                out.append(f"{PATHS[path]} ({path}) never sets {name}, which the api reads")
    steps = step_secrets(root)
    for path in STEPS:
        if path not in steps:
            out.append(f"{WORKFLOW}: no step named {STEPS[path]!r}")
    if len(steps) == len(STEPS):
        for path, secrets in steps.items():
            for other, theirs in steps.items():
                if other == path:
                    continue
                for s in sorted(secrets - theirs - PATH_ONLY.keys()):
                    out.append(
                        f"{WORKFLOW}: {STEPS[path]!r} is given secrets.{s} but {STEPS[other]!r} is not"
                    )
            # what the step is given, its path's files read
            for s in sorted(secrets - PATH_ONLY.keys()):
                if s not in texts[path] and not s.startswith("RELAY_SSH_"):
                    out.append(f"{PATHS[path]} never reads {s}, which {STEPS[path]!r} is given")
    return out


def main() -> int:
    found = gaps()
    for g in found:
        print(f"deploy-secrets: {g}", file=sys.stderr)
    if found:
        return 1
    print("deploy-secrets: both deploy paths wire every secret the api reads")
    return 0


if __name__ == "__main__":
    sys.exit(main())
