"""Story 59.09: the Synology Container Manager project's files (deploy/nas/synology/).

Standard library only (nas.yml's plan job runs it with the runner's python3):

    python3 -m unittest discover -s tests/nas -p 'test_*.py' -v

The files are written by deploy/nas/generate.py from the plain Docker compose; these tests
fail a stale file, a key Container Manager's Docker 24.0.2 does not take, and a drift from
the plain compose. That Docker 24.0.2 runs the file is the synology-compose cell's job.
"""

from __future__ import annotations

import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GEN = ROOT / "deploy/nas/generate.py"
SYN = ROOT / "deploy/nas/synology"
BASE = ROOT / "cloud/installers/docker/docker-compose.yml"

# The service keys the files may use: all read by compose since file format 2.x, so by the
# compose Container Manager ships with Docker 24.0.2 (DSM 7.2)
ALLOWED = {"image", "container_name", "user", "network_mode", "restart", "stop_grace_period", "mem_limit",
           "environment", "volumes", "devices", "group_add"}


def service_keys(text: str) -> dict[str, str]:
    """The service's keys (four spaces in), uncommented, with their inline values."""
    out = {}
    for line in text.splitlines():
        m = re.match(r"^    ([a-z_]+):\s*(.*?)\s*$", line)
        if m:
            out[m.group(1)] = m.group(2)
    return out


def items(text: str, key: str) -> list[str]:
    """The list or map lines under a service key."""
    out, inside = [], False
    for line in text.splitlines():
        if re.match(r"^    [a-z_]+:", line):
            inside = line.strip().startswith(key + ":")
            continue
        if inside and re.match(r"^      [^ #]", line):
            out.append(line.strip())
    return out


class SynologyFiles(unittest.TestCase):
    def test_the_committed_files_are_what_the_generator_writes(self) -> None:
        p = subprocess.run([sys.executable, str(GEN), "--check"], capture_output=True, text=True)
        self.assertEqual(p.returncode, 0, p.stderr)

    def test_only_keys_docker_24_takes(self) -> None:
        for name in ("compose.yaml", "compose.j4125.yaml"):
            text = (SYN / name).read_text(encoding="utf-8")
            self.assertNotRegex(text, r"(?m)^version:", "the top-level version key is obsolete")
            self.assertLessEqual(set(service_keys(text)), ALLOWED, name)

    def test_the_dsm_shape(self) -> None:
        for name in ("compose.yaml", "compose.j4125.yaml"):
            text = (SYN / name).read_text(encoding="utf-8")
            k = service_keys(text)
            self.assertEqual(k["user"], '"1026:100"')
            self.assertEqual(k["container_name"], "maktaba")
            self.assertEqual(k["network_mode"], "host")
            self.assertRegex(k["mem_limit"], r"^(4|6|16)g$")
            self.assertIn("MAKTABA_PLATFORM: dsm", items(text, "environment"))
            vols = items(text, "volumes")
            self.assertIn("- /volume1/docker/maktaba:/var/lib/maktaba", vols)
            # media at its own path (59.13), read-only where Maktaba only reads
            for v in vols[1:]:
                host, inside = v.removeprefix("- ").split(":")[:2]
                self.assertEqual(host, inside, v)
            self.assertTrue(any(v.endswith(":ro") for v in vols))

    def test_only_the_j4125_file_passes_the_gpu(self) -> None:
        j = (SYN / "compose.j4125.yaml").read_text(encoding="utf-8")
        plain = (SYN / "compose.yaml").read_text(encoding="utf-8")
        self.assertEqual(items(j, "devices"), ["- /dev/dri:/dev/dri"])
        self.assertEqual(len(items(j, "group_add")), 1)
        self.assertNotIn("devices", service_keys(plain))
        self.assertNotIn("group_add", service_keys(plain))

    def test_derived_from_the_plain_compose(self) -> None:
        base = service_keys(BASE.read_text(encoding="utf-8"))
        for name in ("compose.yaml", "compose.j4125.yaml"):
            k = service_keys((SYN / name).read_text(encoding="utf-8"))
            for key in ("image", "restart", "stop_grace_period", "network_mode"):
                self.assertEqual(k[key], base[key], f"{name}: {key}")

    def test_the_release_pins_the_tag(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            p = subprocess.run([sys.executable, str(GEN), "--tag", "v0.3.999", "--out", d],
                               capture_output=True, text=True)
            self.assertEqual(p.returncode, 0, p.stderr)
            names = sorted(x.name for x in Path(d).iterdir())
            self.assertEqual(names, ["maktaba-synology-compose.j4125.yaml", "maktaba-synology-compose.yaml"])
            for n in names:
                self.assertIn("image: ghcr.io/hamza-labs-core/maktaba-server:v0.3.999",
                              (Path(d) / n).read_text(encoding="utf-8"))
        self.assertNotEqual(subprocess.run([sys.executable, str(GEN), "--tag", "latest", "--out", "x"],
                                           capture_output=True).returncode, 0)


if __name__ == "__main__":
    unittest.main()
