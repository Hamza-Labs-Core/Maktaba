"""Story 58.10 T03: the Samsung package's build and lint (python3 -m unittest, stdlib only).

    python3 -m unittest discover -s apps/tv/web/tizen -p 'test_*.py'
"""

from __future__ import annotations

import sys
import tempfile
import unittest
import zipfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import lint_wgt  # noqa: E402
import package_wgt  # noqa: E402

INDEX = """<!doctype html>
<html lang="en">
  <head>
    <link rel="stylesheet" href="./assets/style.css">
  </head>
  <body>
    <script defer src="./assets/index.js"></script>
  </body>
</html>
"""


class Built:
    """A launcher build in a temporary folder, packaged."""

    def __init__(self, version: str = "0.3.141") -> None:
        self.tmp = tempfile.TemporaryDirectory()
        root = Path(self.tmp.name)
        dist = root / "dist"
        (dist / "assets").mkdir(parents=True)
        (dist / "index.html").write_text(INDEX, encoding="utf-8")
        (dist / "assets/index.js").write_text("void 0;", encoding="utf-8")
        (dist / "assets/index.js.map").write_text("{}", encoding="utf-8")
        (dist / "assets/style.css").write_text("body{}", encoding="utf-8")
        self.out = root / "out"
        self.stage = package_wgt.stage(dist, version, self.out)
        self.wgt = package_wgt.zip_stage(self.stage, self.out / "m.wgt")

    def rezip(self, edit: dict[str, bytes | None]) -> Path:
        """The package with some files replaced (None: removed)."""
        out = self.out / "edited.wgt"
        with zipfile.ZipFile(self.wgt) as src, zipfile.ZipFile(out, "w") as dst:
            for n in src.namelist():
                if n in edit:
                    continue
                dst.writestr(n, src.read(n))
            for n, data in edit.items():
                if data is not None:
                    dst.writestr(n, data)
        return out

    def config(self) -> str:
        with zipfile.ZipFile(self.wgt) as z:
            return z.read("config.xml").decode()

    def close(self) -> None:
        self.tmp.cleanup()


class PackageTest(unittest.TestCase):
    def setUp(self) -> None:
        self.b = Built()

    def tearDown(self) -> None:
        self.b.close()

    def test_builds_a_package_the_lint_passes(self) -> None:
        self.assertEqual(lint_wgt.lint(self.b.wgt, "0.3.141"), [])

    def test_the_stage_holds_config_icon_and_build_without_source_maps(self) -> None:
        names = sorted(p.relative_to(self.b.stage).as_posix() for p in self.b.stage.rglob("*") if p.is_file())
        self.assertEqual(names, ["assets/index.js", "assets/style.css", "config.xml", "icon.png", "index.html"])
        with zipfile.ZipFile(self.b.wgt) as z:
            self.assertEqual(z.namelist()[0], "config.xml")

    def test_index_loads_webapis_before_the_launcher(self) -> None:
        html = (self.b.stage / "index.html").read_text()
        self.assertLess(html.index("$WEBAPIS/webapis/webapis.js"), html.index("./assets/index.js"))
        self.assertEqual(package_wgt.with_webapis(html), html)

    def test_versions(self) -> None:
        self.assertEqual(package_wgt.tizen_version("v0.3.141"), "0.3.141")
        self.assertEqual(package_wgt.tizen_version("1.2.3"), "1.2.3")
        for bad in ("dev", "1.2", "v1.2.3-rc1", "256.0.0", "0.0.65536"):
            with self.assertRaises(ValueError, msg=bad):
                package_wgt.tizen_version(bad)

    def test_the_build_is_reproducible(self) -> None:
        again = package_wgt.zip_stage(self.b.stage, self.b.out / "again.wgt")
        self.assertEqual(again.read_bytes(), self.b.wgt.read_bytes())

    def test_main_fails_without_a_build(self) -> None:
        with tempfile.TemporaryDirectory() as d:
            rc = package_wgt.main(["--dist", d, "--version", "1.0.0", "--out", d + "/o"])
        self.assertEqual(rc, 1)


class LintTest(unittest.TestCase):
    def setUp(self) -> None:
        self.b = Built()

    def tearDown(self) -> None:
        self.b.close()

    def lint_config(self, old: str, new: str) -> list[str]:
        cfg = self.b.config()
        self.assertIn(old, cfg)
        return lint_wgt.lint(self.b.rezip({"config.xml": cfg.replace(old, new).encode()}))

    def test_an_extra_privilege_fails(self) -> None:
        p = self.lint_config(
            '<access origin="*"',
            '<tizen:privilege name="http://tizen.org/privilege/filesystem.read"/><access origin="*"',
        )
        self.assertEqual(p, ["privilege not listed: http://tizen.org/privilege/filesystem.read"])

    def test_a_missing_privilege_fails(self) -> None:
        p = self.lint_config('<tizen:privilege name="http://tizen.org/privilege/tv.inputdevice"/>', "")
        self.assertEqual(p, ["privilege missing: http://tizen.org/privilege/tv.inputdevice"])

    def test_a_wider_csp_fails(self) -> None:
        p = self.lint_config("script-src 'self' file:", "script-src 'self' file: https:")
        want = ["'self'", "file:"]
        self.assertEqual(p, [f"CSP script-src: {want + ['https:']} (want {want})"])

    def test_a_missing_csp_directive_fails(self) -> None:
        p = self.lint_config("; object-src 'self'", "")
        self.assertEqual(p, ["CSP object-src: None (want [\"'self'\"])"])

    def test_access_and_navigation(self) -> None:
        self.assertEqual(
            self.lint_config('subdomains="true"', 'subdomains="false"'),
            ["access: [('*', 'false')] (want exactly one: origin * with subdomains)"],
        )
        self.assertEqual(
            self.lint_config("<tizen:allow-navigation>*<", "<tizen:allow-navigation>maktaba.tv<"),
            ["tizen:allow-navigation: ['maktaba.tv'] (want *)"],
        )

    def test_the_app_id_and_oldest_tizen(self) -> None:
        self.assertEqual(
            self.lint_config('required_version="5.5"', 'required_version="4.0"'),
            ["tizen:application required_version: '4.0' (want '5.5')"],
        )

    def test_the_version(self) -> None:
        self.assertEqual(lint_wgt.lint(self.b.wgt, "0.3.142"), ["version: 0.3.141 (want 0.3.142)"])

    def test_files(self) -> None:
        self.assertEqual(
            lint_wgt.lint(self.b.rezip({"icon.png": None})),
            ["icon: 'icon.png' missing from the package"],
        )
        self.assertEqual(
            lint_wgt.lint(self.b.rezip({"assets/index.js": None})),
            ["index.html: assets/index.js is not in the package"],
        )
        self.assertEqual(
            lint_wgt.lint(self.b.rezip({"index.html": INDEX.encode()})),
            ["index.html: does not load webapis.js"],
        )
        self.assertEqual(
            lint_wgt.lint(self.b.rezip({"assets/index.js.map": b"{}"})),
            ["assets/index.js.map: a source map in the package"],
        )
        self.assertEqual(
            lint_wgt.lint(self.b.rezip({"icon.png": b"\x89PNG\r\n\x1a\n\0\0\0\rIHDR\0\0\0\x10\0\0\0\x10"})),
            ["icon: (16, 16) (want a 512x423 PNG)"],
        )

    def test_signed(self) -> None:
        self.assertEqual(
            lint_wgt.lint(self.b.wgt, signed=True),
            [
                "author-signature.xml: missing (the package is not signed)",
                "signature1.xml: missing (the package is not signed)",
            ],
        )
        signed = self.b.rezip({"author-signature.xml": b"<x/>", "signature1.xml": b"<x/>"})
        self.assertEqual(lint_wgt.lint(signed, signed=True), [])

    def test_not_a_package(self) -> None:
        bad = self.b.out / "bad.wgt"
        bad.write_text("no")
        self.assertEqual(len(lint_wgt.lint(bad)), 1)
        self.assertEqual(lint_wgt.main([str(bad)]), 1)


if __name__ == "__main__":
    unittest.main()
