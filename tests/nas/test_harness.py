"""The nas-shapes harness's own tests (story 59.15): the matrix, the plan, the profiles, the
checklists' lint. Standard library only: nas.yml's plan job runs them with the runner's python3.

    python3 -m unittest discover -s tests/nas -p 'test_*.py' -v
"""

from __future__ import annotations

import json
import sys
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import cell  # noqa: E402
import check_checklists  # noqa: E402
import plan  # noqa: E402
import summary  # noqa: E402

MATRIX = cell.load_cells()
# Every story whose cells and assertions the harness knows (README §7 phases); a new needs
# value is a typo unless it is added here with its cell.
KNOWN_NEEDS = {"59.01", "59.02", "59.03", "59.04", "59.05", "59.06", "59.07", "59.08", "59.10", "59.11", "pg-stale-lock", "scan-grant-race", "plugin-cache-grant", "poster-queue"}
# The phase-1 cells story 59.15 lists (README §9): each must be in the matrix
PHASE_ONE = {"uid-1000", "uid-568", "uid-unraid", "uid-dsm", "media-ro", "media-none", "media-foreign",
             "mem-4g", "mem-6g", "no-landlock-enosys", "no-landlock-eperm"}


class Matrix(unittest.TestCase):
    def test_names_are_unique_and_docker_safe(self) -> None:
        names = [c["name"] for c in MATRIX["cells"]]
        self.assertEqual(len(names), len(set(names)))
        for n in names:
            self.assertRegex(n, r"^[a-z0-9][a-z0-9.-]*$")

    def test_phase_one_cells_are_there(self) -> None:
        self.assertLessEqual(PHASE_ONE, {c["name"] for c in MATRIX["cells"]})

    def test_uid_cells_run_on_both_architectures(self) -> None:
        for c in MATRIX["cells"]:
            if c["name"] in ("uid-1000", "uid-568", "uid-unraid", "uid-dsm"):
                self.assertEqual(sorted(c["arches"]), ["amd64", "arm64"], c["name"])

    def test_every_assertion_exists(self) -> None:
        for c in MATRIX["cells"]:
            self.assertTrue(c.get("asserts"), c["name"])
            for a in c["asserts"]:
                name = cell.assertion_name(a)
                pending = cell.assertion_needs(a) or c.get("needs")
                self.assertTrue(callable(getattr(cell.Cell, "assert_" + name, None)) or pending,
                                f"{c['name']}: no assert_{name} (only a pending assertion may wait for its code)")

    def test_needs_are_known_and_landed_is_a_subset(self) -> None:
        self.assertLessEqual(set(MATRIX["landed"]), KNOWN_NEEDS)
        for c in MATRIX["cells"]:
            needs = set(c.get("needs", []))
            for a in c["asserts"]:
                needs |= set(cell.assertion_needs(a))
            self.assertLessEqual(needs, KNOWN_NEEDS, c["name"])

    def test_every_landed_assertion_has_its_code(self) -> None:
        landed = set(MATRIX["landed"])
        for c in MATRIX["cells"]:
            if not set(c.get("needs", [])) <= landed:
                continue
            for a in c["asserts"]:
                if set(cell.assertion_needs(a)) <= landed:
                    self.assertTrue(hasattr(cell.Cell, "assert_" + cell.assertion_name(a)),
                                    f"{c['name']}: {cell.assertion_name(a)} landed with no code")

    def test_seccomp_profiles_exist(self) -> None:
        for c in MATRIX["cells"]:
            if c.get("seccomp"):
                self.assertTrue((HERE / c["seccomp"]).is_file(), c["seccomp"])

    def test_refused_cells_assert_a_refusal(self) -> None:
        for c in MATRIX["cells"]:
            if c.get("expect") == "refused":
                self.assertTrue(all(cell.assertion_name(a).startswith("refused_") for a in c["asserts"]), c["name"])


class Plan(unittest.TestCase):
    def test_a_cell_waits_for_its_story(self) -> None:
        m = {"landed": [], "cells": [{"name": "a", "arches": ["amd64"]}, {"name": "b", "needs": ["59.01"]}]}
        self.assertEqual([e["cell"] for e in plan.plan(m)], ["a"])
        m["landed"] = ["59.01"]
        self.assertEqual([e["cell"] for e in plan.plan(m)], ["a", "b"])

    def test_arm64_runner_and_no_arm64(self) -> None:
        m = {"landed": [], "cells": [{"name": "a", "arches": ["amd64", "arm64"]}]}
        self.assertEqual(plan.plan(m), [{"cell": "a", "arch": "amd64", "runner": "ubuntu-24.04"},
                                        {"cell": "a", "arch": "arm64", "runner": "ubuntu-24.04-arm"}])
        self.assertEqual([e["arch"] for e in plan.plan(m, arm64=False)], ["amd64"])

    def test_only(self) -> None:
        self.assertEqual({e["cell"] for e in plan.plan(MATRIX, only={"uid-1000"})}, {"uid-1000"})

    def test_the_real_matrix_has_a_baseline(self) -> None:
        cells = {(e["cell"], e["arch"]) for e in plan.plan(MATRIX)}
        self.assertIn(("uid-1000", "amd64"), cells)
        self.assertIn(("uid-1000", "arm64"), cells)


class Pending(unittest.TestCase):
    def test_an_assertion_whose_story_has_not_landed_is_pending(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x"}, image="img", fixtures=Path(d), out=Path(d), local_port=None, landed=[])
            c.check({"name": "tier_light", "needs": ["59.04"]})
            self.assertEqual(c.results[-1]["ok"], None)
            self.assertIn("59.04", c.results[-1]["detail"])

    def test_a_pending_cell_passes_without_docker(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x", "needs": ["59.01"], "asserts": ["ready"]}, image="img",
                          fixtures=Path(d), out=Path(d) / "out", local_port=None, landed=[])
            self.assertTrue(c.run())
            report = json.loads((Path(d) / "out" / "report.json").read_text(encoding="utf-8"))
            self.assertIsNone(report["results"][0]["ok"])

    def test_an_unknown_assertion_fails(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            c = cell.Cell(spec={"name": "x"}, image="img", fixtures=Path(d), out=Path(d), local_port=None, landed=[])
            c.check("no_such_thing")
            self.assertIs(c.results[-1]["ok"], False)


class Networking(unittest.TestCase):
    """Story 59.08: the mDNS cells and the bridge cell's shapes."""

    def mk(self, spec: dict) -> cell.Cell:
        return cell.Cell(spec={"name": "n", **spec}, image="img", fixtures=Path("."), out=Path("."),
                         local_port=None, landed=[])

    def test_the_dbus_cell_mounts_the_nas_bus_and_the_sibling_cell_does_not(self) -> None:
        self.assertIn("nascell-n-dbus:/run/dbus", self.mk({"avahi": "dbus"}).shape())
        self.assertNotIn("nascell-n-dbus:/run/dbus", self.mk({"avahi": "sibling"}).shape())
        self.assertEqual(self.mk({"avahi": "sibling"}).network(), ["--network", "host"])

    def test_the_bridge_cell_publishes_its_ports_but_not_postgres(self) -> None:
        c = self.mk({"network": "bridge"})
        net = c.network()
        self.assertNotIn("--network", net)
        for p in ("8088:8088", "8080:8080", "8081:8081"):
            self.assertIn(p, net)
        self.assertFalse(any("54329" in a for a in net))
        self.assertEqual(c.setup_network(), [])
        self.assertTrue(any(a.startswith("MAKTABA_HOST_IP=") for a in c.shape()))
        self.assertEqual(self.mk({}).setup_network(), ["--network", "host"])

    def test_browse_asks_for_raw_service_types(self) -> None:
        # without -k avahi-browse prints _smb._tcp as "Microsoft Windows Network", and the
        # mDNS cells waited 300 s for a type they never saw (the first CI run of #1119)
        c = self.mk({"avahi": "sibling"})
        cmds: list[str] = []
        line = "=;eth0;IPv4;NAS share;_smb._tcp;local;nas.local;10.0.0.2;445;"

        def sh(cmd: str) -> str:
            cmds.append(cmd)
            return line + "\n+;eth0;IPv4;NAS share;_smb._tcp;local\n"

        c.avahi_sh = sh  # type: ignore[method-assign]
        self.assertEqual(c.browse(), [line.split(";")])
        self.assertIn(" -k", cmds[0])

    def test_the_networking_cells_are_in_the_matrix(self) -> None:
        names = {c["name"]: c for c in cell.load_cells()["cells"]}
        for n in ("mdns-own", "mdns-dbus", "bridge"):
            self.assertIn(n, names)
            self.assertIn("59.08", names[n]["needs"])


class ServerToml(unittest.TestCase):
    def test_no_quiet_hours_so_a_daytime_first_scan_runs(self) -> None:
        import tomllib

        conf = tomllib.loads(cell.SERVER_TOML)
        self.assertEqual(conf["schedule"]["quiet_hours"], "")

    def test_the_hold_cell_gets_a_window_that_opens_later(self) -> None:
        from datetime import datetime, timezone

        import tomllib

        now = datetime(2026, 11, 10, 22, 41, tzinfo=timezone.utc)
        window, opens = cell.quiet_window_ahead(now)
        self.assertEqual((window, opens), ("01:00-02:00", "01:00"))  # across midnight, now outside
        conf = tomllib.loads(cell.server_toml(window))
        self.assertEqual(conf["schedule"]["quiet_hours"], "01:00-02:00")


class PostgresPss(unittest.TestCase):
    def test_sums_every_postgres_process(self) -> None:
        listing = "41 Pss:              10240 kB\n57 Pss: 2048 kB\n58 \n"
        self.assertEqual(cell.pss_sum(listing), (12288, 2))

    def test_nothing_read_is_zero_processes(self) -> None:
        self.assertEqual(cell.pss_sum(""), (0, 0))


class Summary(unittest.TestCase):
    def test_rows_link_each_cell_to_its_logs(self) -> None:
        jobs = summary.objects('{"jobs": [{"name": "cell uid-568 (arm64)", "conclusion": "failure", "html_url": "u"},'
                               ' {"name": "plan", "conclusion": "success"}]}\n{"jobs": [{"name": "cell mem-3.7g (amd64)",'
                               ' "conclusion": "success", "html_url": "v"}]}')
        arts = [{"name": "nas-cell-uid-568-arm64", "id": 7}]
        got = summary.rows([j for d in jobs for j in d["jobs"]], arts, "o/r", "9")
        self.assertEqual(got, ["| [cell mem-3.7g (amd64)](v) | success | none |",
                               "| [cell uid-568 (arm64)](u) | failure | [logs](https://github.com/o/r/actions/runs/9/artifacts/7) |"])


class Seccomp(unittest.TestCase):
    def load(self, name: str) -> dict:
        return json.loads((HERE / f"seccomp-no-landlock-{name}.json").read_text(encoding="utf-8"))

    def rule(self, profile: dict, syscall: str) -> dict:
        return next(r for r in profile["syscalls"] if syscall in r["names"])

    def test_landlock_answers(self) -> None:
        for name, errno in (("enosys", 38), ("eperm", 1)):
            p = self.load(name)
            self.assertEqual(p["defaultAction"], "SCMP_ACT_ALLOW")
            for sc in ("landlock_create_ruleset", "landlock_add_rule", "landlock_restrict_self"):
                r = self.rule(p, sc)
                self.assertEqual((r["action"], r["errnoRet"]), ("SCMP_ACT_ERRNO", errno), (name, sc))

    def test_no_user_namespaces(self) -> None:
        for name in ("enosys", "eperm"):
            p = self.load(name)
            self.assertEqual(self.rule(p, "unshare")["errnoRet"], 1)
            clone = self.rule(p, "clone")
            self.assertEqual(clone["args"][0]["value"], 0x10000000)  # CLONE_NEWUSER
            self.assertEqual(self.rule(p, "clone3")["errnoRet"], 38)


class Checklists(unittest.TestCase):
    STEP = "### 1. Do a thing\n- **Do:** press it\n- **See:** it is pressed\n- **Screenshots:** {shots}\n"

    def write(self, d: Path, body: str, status: str = "draft") -> None:
        (d / "x.md").write_text(f"---\nplatform: x\ndevices: one\nstatus: {status}\n---\n\n# X\n\n{body}",
                                encoding="utf-8")

    def test_the_real_checklists_pass(self) -> None:
        self.assertEqual(check_checklists.check_dir(HERE.parents[1] / "docs" / "nas" / "checklists"), [])

    def test_a_step_without_its_expected_result_fails(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, "### 1. Do a thing\n- **Do:** press it\n- **Screenshots:** none\n")
            self.assertTrue(any("no See" in e for e in check_checklists.check_dir(d)))

    def test_screenshots_come_in_both_languages(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="`x/1.en.png`"))
            self.assertTrue(any("pairs" in e for e in check_checklists.check_dir(d)))
            self.write(d, self.STEP.format(shots="`x/1.en.png`, `x/1.ar.png`"))
            self.assertEqual(check_checklists.check_dir(d), [])

    def test_a_ready_checklist_has_its_screenshots(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="`x/1.en.png`, `x/1.ar.png`"), status="ready")
            self.assertEqual(len(check_checklists.check_dir(d)), 2)
            (d / "screenshots" / "x").mkdir(parents=True)
            for lang in ("en", "ar"):
                (d / "screenshots" / "x" / f"1.{lang}.png").write_bytes(b"\x89PNG")
            self.assertEqual(check_checklists.check_dir(d), [])

    def test_steps_are_numbered_without_a_gap(self) -> None:
        with tempfile.TemporaryDirectory() as t:
            d = Path(t)
            self.write(d, self.STEP.format(shots="none") + self.STEP.format(shots="none").replace("### 1.", "### 3."))
            self.assertTrue(any("expected 2" in e for e in check_checklists.check_dir(d)))


if __name__ == "__main__":
    unittest.main()


class SpeechTest(unittest.TestCase):
    """Story 59.06: the speech engine's pins and the word error rate's baseline."""

    CONSTRAINTS = (
        "# This file was autogenerated by uv\n"
        "--extra-index-url https://example.invalid/simple\n"
        "av==19.0.2 ; (platform_machine == 'aarch64' and sys_platform == 'linux') or (platform_machine == 'x86_64') \\\n"
        "    --hash=sha256:aa \\\n"
        "    --hash=sha256:bb\n"
        "    # via faster-whisper\n"
        "numpy==2.5.4 \\\n"
        "    --hash=sha256:cc\n"
        "torch==2.14.1 ; (platform_machine == 'x86_64' and sys_platform == 'linux') \\\n"
        "    --hash=sha256:dd\n"
        "ctranslate2==4.8.3 ; (platform_machine == 'aarch64' and sys_platform == 'linux') \\\n"
        "    --hash=sha256:ee\n"
    )

    def test_the_engine_is_the_aarch64_pins_without_markers(self) -> None:
        reqs = cell.stt_engine_requirements(self.CONSTRAINTS)
        heads = [line for line in reqs.splitlines() if line and not line[0].isspace()]
        self.assertEqual(heads, ["av==19.0.2 \\", "numpy==2.5.4 \\", "ctranslate2==4.8.3 \\"])
        self.assertIn("    --hash=sha256:bb", reqs)
        self.assertNotIn("torch", reqs)

    def test_a_rise_over_five_points_fails(self) -> None:
        base = {"base/aarch64": {"en": 0.09, "ar": 0.46}}
        ok = [{"lang": "en", "wer": 0.13}, {"lang": "ar", "wer": 0.40}]
        self.assertEqual(cell.wer_rise(base, "base/aarch64", ok), [])
        bad = [{"lang": "ar", "wer": 0.52}]
        self.assertEqual(len(cell.wer_rise(base, "base/aarch64", bad)), 1)
        self.assertEqual(cell.wer_rise(base, "small/x86_64", bad), [])  # no baseline yet

    def test_the_baseline_file_reads(self) -> None:
        doc = json.loads(cell.STT_BASELINE.read_text(encoding="utf-8"))
        for key, rates in doc.items():
            if key.startswith("_"):
                continue
            self.assertRegex(key, r"^[a-z0-9.-]+/(aarch64|x86_64)$")
            self.assertTrue(set(rates) <= {"en", "ar"} and all(0 <= v <= 2 for v in rates.values()))

    def test_a_speech_cell_turns_speech_on(self) -> None:
        self.assertIn('speech = "on"', cell.server_toml("", speech=True))
        self.assertNotIn("faster-whisper-tiny", cell.server_toml("", speech=True))
        self.assertIn("faster-whisper-tiny", cell.server_toml(""))


class TrueNASShape(unittest.TestCase):
    """Story 59.10: the shape truenas_app.py asks of each render of the TrueNAS app."""

    def setUp(self) -> None:
        import truenas_app

        self.check = truenas_app.check_shape
        self.values = {
            "TZ": "Asia/Riyadh",
            "run_as": {"user": 568, "group": 568},
            "network": {"host_network": True},
            "resources": {"limits": {"memory": 4096}, "gpus": {}},
            "storage": {"data": {"type": "ix_volume"}, "media": [{"path": "/mnt/tank/media", "read_only": True}]},
        }

    def compose(self) -> dict:
        return {"services": {
            "permissions": {"user": "0:0"},
            "maktaba": {
                "user": "568:568", "network_mode": "host", "stop_grace_period": "1m30s",
                "environment": {"MAKTABA_PLATFORM": "truenas", "TZ": "Asia/Riyadh", "NVIDIA_VISIBLE_DEVICES": "void"},
                "healthcheck": {"test": ["CMD", "/opt/maktaba/server/bin/maktaba-server", "healthcheck"]},
                "volumes": [{"type": "bind", "source": "/opt/x", "target": "/var/lib/maktaba"},
                            {"type": "bind", "source": "/mnt/tank/media", "target": "/mnt/tank/media", "read_only": True}],
                "depends_on": {"permissions": {"condition": "service_completed_successfully"}},
                "group_add": [568],
                "deploy": {"resources": {"limits": {"memory": str(4096 << 20)}}},
            }}}

    def test_the_default_render_passes(self) -> None:
        self.assertIn("568:568, host network, ix_volume data, 1 media, no GPU", self.check(self.compose(), self.values))

    def test_each_departure_is_named(self) -> None:
        cases = [
            (lambda s: s.update(user="1000:1000"), "user"),
            (lambda s: s["environment"].pop("MAKTABA_PLATFORM"), "MAKTABA_PLATFORM"),
            (lambda s: s["environment"].update(MAKTABA_UPDATE_DISABLE="1"), "self-update"),
            (lambda s: s.pop("network_mode"), "network_mode"),
            (lambda s: s["volumes"].append({"source": "/run/dbus/system_bus_socket", "target": "/run/dbus/system_bus_socket"}), "D-Bus"),
            (lambda s: s["volumes"].append({"source": "/dev/null", "target": "/proc/spl/kstat/zfs/arcstats"}), "/proc"),
            (lambda s: s["volumes"][1].update(read_only=False), "read_only"),
            (lambda s: s["volumes"][1].update(target="/media"), "own path"),
            (lambda s: s.pop("depends_on"), "permissions step"),
            (lambda s: s.update(devices=["/dev/dri:/dev/dri"]), "/dev/dri"),
            (lambda s: s.update(stop_grace_period="10s"), "stop_grace_period"),
        ]
        for change, words in cases:
            c = self.compose()
            change(c["services"]["maktaba"])
            with self.assertRaisesRegex(AssertionError, words.replace("/", "\\/")):
                self.check(c, self.values)

    def test_gpus_bridge_and_host_path(self) -> None:
        v = dict(self.values, resources={"limits": {"memory": 4096}, "gpus": {"use_all_gpus": True}})
        c = self.compose()
        c["services"]["maktaba"].update(devices=[{"source": "/dev/dri", "target": "/dev/dri"}], group_add=[44, 107, 568])
        self.assertIn("Intel/AMD GPU", self.check(c, v))
        c["services"]["maktaba"]["group_add"] = [568]
        with self.assertRaisesRegex(AssertionError, "render"):
            self.check(c, v)

        v = dict(self.values, resources={"limits": {"memory": 4096}, "gpus": {
            "nvidia_gpu_selection": {"0000:01:00.0": {"use_gpu": True, "uuid": "GPU-x"}}}})
        c = self.compose()
        s = c["services"]["maktaba"]
        s["deploy"]["resources"]["reservations"] = {"devices": [{"driver": "nvidia", "capabilities": ["gpu"]}]}
        s["environment"]["NVIDIA_VISIBLE_DEVICES"] = "GPU-x"
        self.assertIn("NVIDIA", self.check(c, v))

        v = dict(self.values, network={"host_network": False, "host_ip": "192.168.1.20"})
        c = self.compose()
        s = c["services"]["maktaba"]
        s.pop("network_mode")
        s["ports"] = [{"target": t, "published": str(30000 + t % 1000)} for t in (8088, 8080, 8081)]
        s["environment"]["MAKTABA_HOST_IP"] = "192.168.1.20"
        self.assertIn("bridge", self.check(c, v))
        s["ports"].append({"target": 54329})
        with self.assertRaisesRegex(AssertionError, "Postgres"):
            self.check(c, v)

        v = dict(self.values, storage={"data": {"type": "host_path"}, "media": []})
        c = self.compose()
        c["services"]["maktaba"]["volumes"].pop()
        with self.assertRaisesRegex(AssertionError, "never chowned"):
            self.check(c, v)
        del c["services"]["permissions"]
        c["services"]["maktaba"].pop("depends_on")
        self.assertIn("host_path data", self.check(c, v))
