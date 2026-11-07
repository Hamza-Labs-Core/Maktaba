"""Story 54.01 §3: the shared manifest corpus is whole. Each language's unit job runs it
through its own validator against the committed normalised JSON (Go
shared/plugins/pluginmanifest, Rust plugind/crates/pluginmanifest, Python
pipeline/tests/plugins/test_manifest.py, TypeScript tools/plugin-sdk/js), so the four agree
byte for byte through the goldens. This checks the corpus they share: every invalid case
names its code, every valid case has its golden, and the copies are the shared files."""

from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SHARED = ROOT / "shared" / "plugins"
CORPUS = SHARED / "testdata" / "manifests"
CODE = re.compile(r"^# expect: (manifest_[a-z_]+|plugin_trusted_field)( [a-z0-9/._-]+)?$")


def test_every_invalid_case_names_one_code() -> None:
    cases = sorted((CORPUS / "invalid").glob("*.toml"))
    assert len(cases) >= 40
    for c in cases:
        first = c.read_text().splitlines()[0]
        assert CODE.match(first), f"{c.name}: {first!r}"


def test_every_valid_case_has_its_golden() -> None:
    valid = {p.stem for p in (CORPUS / "valid").glob("*.toml")}
    goldens = {p.stem for p in (CORPUS / "normalised").glob("*.json")}
    assert valid and valid == goldens
    for g in (CORPUS / "normalised").glob("*.json"):
        text = g.read_text().rstrip("\n")
        assert json.dumps(json.loads(text), sort_keys=True, separators=(",", ":"), ensure_ascii=False) == text, g.name


def test_every_code_has_english_and_arabic_words() -> None:
    en = json.loads((ROOT / "web" / "src" / "i18n" / "en.json").read_text())
    ar = json.loads((ROOT / "web" / "src" / "i18n" / "ar.json").read_text())
    codes = {CODE.match(c.read_text().splitlines()[0]).group(1) for c in (CORPUS / "invalid").glob("*.toml")}  # type: ignore[union-attr]
    for code in sorted(codes):
        assert f"plugins.error.{code}" in en and f"plugins.error.{code}" in ar, code


def test_copies_are_the_shared_files() -> None:
    data = ROOT / "pipeline" / "src" / "maktaba_pipeline" / "plugins" / "data"
    for name in ("manifest.schema.json", "capabilities.toml", "events.toml"):
        assert (data / name).read_bytes() == (SHARED / name).read_bytes(), name
    generated = (ROOT / "tools" / "plugin-sdk" / "js" / "src" / "data.generated.ts").read_text()
    for name in ("manifest.schema.json", "capabilities.toml", "events.toml"):
        assert json.dumps((SHARED / name).read_text(), ensure_ascii=False) in generated, name


def test_capability_words_exist() -> None:
    import tomllib

    en = json.loads((ROOT / "web" / "src" / "i18n" / "en.json").read_text())
    ar = json.loads((ROOT / "web" / "src" / "i18n" / "ar.json").read_text())
    caps = tomllib.loads((SHARED / "capabilities.toml").read_text())["capability"]
    for c in caps:
        for key in (c["title_key"], c["detail_key"]):
            assert key in en and key in ar, key
