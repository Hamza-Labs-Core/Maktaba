"""The NAS install files' checks (story 59.11, T01, T03, T04) and the cells that start them.
Standard library only: nas.yml's plan job runs them (the `docker compose config` and registry
legs run there too, as `install_files.py --docker`).

    python3 -m unittest discover -s tests/nas -p 'test_*.py' -v
"""

from __future__ import annotations

import sys
import tempfile
import unittest
from dataclasses import replace
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import cell  # noqa: E402
import install_files as f  # noqa: E402

COMPOSE = f.COMPOSE.read_text(encoding="utf-8")
ROOT = HERE.parents[1]


class Compose(unittest.TestCase):
    def test_the_published_file_is_the_nas_base(self) -> None:
        self.assertEqual(f.check_compose(COMPOSE), [])

    def test_every_block_has_settings(self) -> None:
        found = f.blocks(COMPOSE)
        for n in f.BLOCKS:
            self.assertTrue(found.get(n), n)

    def test_explanations_stay_comments(self) -> None:
        on = f.enable(COMPOSE, f.BLOCKS)
        self.assertIn("    # [dri] Hardware video on an Intel or AMD chip", on)
        self.assertIn("    # render device and its group", on)
        self.assertIn("    mem_limit: 6g", on)
        self.assertIn("      - /dev/dri:/dev/dri", on)
        self.assertIn("              capabilities: [gpu]", on)

    def test_the_bridge_block_drops_the_host_network(self) -> None:
        on = f.enable(COMPOSE, ("bridge",))
        self.assertNotIn("network_mode: host", on)
        self.assertIn('      - "8088:8088"', on)
        self.assertIn("      MAKTABA_HOST_IP:", on)
        self.assertNotIn("54329:54329", on)

    def test_an_unknown_block_is_an_error(self) -> None:
        with self.assertRaises(ValueError):
            f.enable(COMPOSE, ("nope",))

    def test_a_catch_all_media_mount_fails(self) -> None:
        bad = COMPOSE.replace("- /srv/media:/srv/media", "- ./media:/media")
        self.assertTrue(any("own path" in e for e in f.check_compose(bad)))

    def test_variants_are_none_each_and_all(self) -> None:
        names = [n for n, _ in f.variants()]
        self.assertEqual(names[0], "plain")
        self.assertEqual(names[-1], "all")
        self.assertEqual(set(names[1:-1]), set(f.BLOCKS))


class Unraid(unittest.TestCase):
    def setUp(self) -> None:
        self.t = f.read_template()

    def test_the_template_is_valid(self) -> None:
        self.assertEqual(f.check_template(self.t), [])

    def test_it_renders_as_dockerman_runs_it(self) -> None:
        args = f.render(self.t)
        self.assertEqual(args[:2], ["--net", "host"])
        joined = " ".join(args)
        self.assertIn("--user 99:100", joined)
        self.assertIn("--stop-timeout 90", joined)
        self.assertIn("-e MAKTABA_PLATFORM=unraid", joined)
        self.assertIn("-v /mnt/cache/appdata/maktaba:/var/lib/maktaba:rw", joined)
        self.assertIn("-v /mnt/user/Media:/mnt/user/Media:rw", joined)
        self.assertIn("--device /dev/dri", joined)
        self.assertEqual(args[-1], "ghcr.io/hamza-labs-core/maktaba-server:latest")

    def test_no_device_where_the_runner_has_none(self) -> None:
        self.assertNotIn("--device", f.render(self.t, devices=False))

    def test_it_matches_the_compose_file(self) -> None:
        self.assertEqual(f.check_drift(COMPOSE, self.t), [])

    def test_drift_is_caught(self) -> None:
        moved = replace(self.t, fields={**self.t.fields, "ExtraParams": "--user 99:100 --stop-timeout 10"})
        self.assertTrue(f.check_drift(COMPOSE, moved))
        other = replace(self.t, fields={**self.t.fields, "Repository": "ghcr.io/someone/else:latest"})
        self.assertTrue(f.check_drift(COMPOSE, other))
        self.assertTrue(f.check_template(other))

    def test_data_on_the_array_is_refused(self) -> None:
        bad = replace(self.t, configs=[replace(c, default="/mnt/user/appdata/maktaba", value="/mnt/user/appdata/maktaba")
                                       if c.target == f.DATA else c for c in self.t.configs])
        self.assertTrue(any("cache pool" in e for e in f.check_template(bad)))

    def test_the_support_link_is_maktaba_tv_itself(self) -> None:
        for url in ("https://maktaba.tv.evil.example/x", "http://maktaba.tv/x", "https://evil.example/https://maktaba.tv"):
            bad = replace(self.t, fields={**self.t.fields, "Support": url})
            self.assertTrue(any("support link" in e for e in f.check_template(bad)), url)

    def test_root_is_refused(self) -> None:
        bad = replace(self.t, fields={**self.t.fields, "ExtraParams": "--stop-timeout 90"})
        self.assertTrue(any("99:100" in e for e in f.check_template(bad)))


class Dev(unittest.TestCase):
    def test_the_dev_images_are_published_names(self) -> None:
        self.assertEqual(f.check_dev(), [])
        self.assertEqual(f.dev_images(), {s: f"ghcr.io/hamza-labs-core/maktaba-{s}"
                                          for s in ("api", "pipeline", "streaming", "web")})

    def test_a_wrong_name_fails(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / "c.yml"
            p.write_text(f.DEV_COMPOSE.read_text(encoding="utf-8").replace(
                "ghcr.io/hamza-labs-core/maktaba-api:", "ghcr.io/maktaba/api:"), encoding="utf-8")
            self.assertTrue(any("api" in e for e in f.check_dev(p)))

    def test_release_publishes_the_four(self) -> None:
        self.assertLessEqual({"api", "streaming", "pipeline", "web"}, set(f.published_services()))

    def test_the_old_scaffolds_are_gone(self) -> None:
        self.assertFalse((ROOT / "cloud/installers/nas").exists())


class Updates(unittest.TestCase):
    """Story 59.12: the image's self-update and a NAS catalog's live side by side. No install
    file turns the self-update off; the files a household copies name `:latest` (the highest
    stable release), and the release pins every NAS file it publishes to its own tag."""

    FILES = ("cloud/installers/docker/docker-compose.yml", "deploy/nas/synology/compose.yaml",
             "deploy/nas/synology/compose.j4125.yaml", "deploy/nas/unraid/maktaba.xml",
             "deploy/nas/truenas/custom-app.yaml")

    def test_no_install_file_turns_the_self_update_off(self) -> None:
        for name in self.FILES:
            for line in (ROOT / name).read_text(encoding="utf-8").splitlines():
                if "MAKTABA_UPDATE_DISABLE" in line:
                    self.assertTrue(line.lstrip().startswith("#") or "<!--" in line, f"{name}: {line.strip()}")
        for path in sorted((ROOT / "deploy/nas/truenas/ix-dev").rglob("*")):
            if path.is_file():
                self.assertNotIn("MAKTABA_UPDATE_DISABLE", path.read_text(encoding="utf-8", errors="replace"), path)

    def test_the_copied_files_name_latest(self) -> None:
        for name in self.FILES[:4]:
            text = (ROOT / name).read_text(encoding="utf-8")
            self.assertIn("ghcr.io/hamza-labs-core/maktaba-server:latest", text, name)

    def test_the_release_pins_every_nas_file(self) -> None:
        release = (ROOT / ".github/workflows/release.yml").read_text(encoding="utf-8")
        # the published compose, rewritten to the tag
        self.assertIn('sed "s|maktaba-server:latest|maktaba-server:${TAG}|" cloud/installers/docker/docker-compose.yml', release)
        # the Synology files, made from that compose with the same pin
        self.assertIn('python3 deploy/nas/generate.py --tag "$TAG" --out dist', release)
        # the TrueNAS app and its Custom App YAML, pinned to the tag and the image's digest
        self.assertIn('python3 tools/release/truenas_app.py render --tag "${TAG}" --digest "${server_digest}"', release)
        # Unraid's template stays on :latest (its update check moves it); it is not a release asset
        self.assertNotIn("maktaba.xml", release)


class Cells(unittest.TestCase):
    def cell(self, spec: dict, d: str) -> cell.Cell:
        return cell.Cell(spec=spec, image="maktaba-server:nas", fixtures=Path(d), out=Path(d), local_port=None,
                         landed=["59.11"])

    def test_the_compose_cell_fills_the_published_file_in(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = self.cell({"name": "compose-plain", "user": "1000:100", "compose": {"blocks": ["memory"]}}, d)
            text = c.compose_text()
            self.assertIn("image: maktaba-server:nas", text)
            self.assertIn("container_name: nascell-compose-plain", text)
            self.assertIn('user: "1000:100"', text)
            self.assertIn("    mem_limit: 6g", text)
            self.assertIn(f"- data:{cell.VOLUME}", text)
            self.assertIn(f"- media:{cell.MEDIA}", text)
            self.assertIn("name: nascell-compose-plain-data", text)
            self.assertIn("network_mode: host", text)

    def test_a_compose_cell_refuses_a_docker_shape(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = self.cell({"name": "x", "compose": {}, "docker": ["--memory=4g"]}, d)
            with self.assertRaises(AssertionError):
                c.compose_text()

    def test_the_unraid_cell_runs_the_template(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = self.cell({"name": "uid-unraid", "template": "unraid"}, d)
            self.assertEqual(c.user, "99:100")
            a = c.shape()
            joined = " ".join(a)
            self.assertNotIn("--net", a)
            self.assertIn("--user 99:100", joined)
            self.assertIn("-e MAKTABA_PLATFORM=unraid", joined)
            self.assertIn(f"-v nascell-uid-unraid-data:{cell.VOLUME}:rw", joined)
            self.assertIn(f"-v nascell-uid-unraid-media:{cell.MEDIA}:rw", joined)
            self.assertNotEqual(a[-1], "-")

    def test_the_unraid_cell_user_must_be_the_templates(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = self.cell({"name": "x", "template": "unraid", "user": "1000:1000"}, d)
            with self.assertRaises(AssertionError):
                c.unraid_shape()

    def test_the_matrix_starts_both(self) -> None:
        cells = {c["name"]: c for c in cell.load_cells()["cells"]}
        self.assertEqual(cells["uid-unraid"].get("template"), "unraid")
        self.assertIn("compose", cells["compose-plain"])
        self.assertIn("59.11", cell.load_cells()["landed"])


if __name__ == "__main__":
    unittest.main()
