"""Story 54.01 T01, T02, T05 (python): the shared corpus through the pipeline's
validator, byte-identical normalised JSON, and the queue name vector."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from maktaba_pipeline.plugins import manifest

pytestmark = pytest.mark.unit

ROOT = Path(__file__).resolve().parents[3]
CORPUS = ROOT / "shared" / "plugins" / "testdata" / "manifests"
DATA = ROOT / "pipeline" / "src" / "maktaba_pipeline" / "plugins" / "data"


def _files(path: str) -> bytes | None:
    p = CORPUS / "package" / path
    return p.read_bytes() if p.is_file() else None


@pytest.mark.parametrize("case", sorted((CORPUS / "valid").glob("*.toml")), ids=lambda p: p.stem)
def test_valid_corpus_normalises_to_the_golden(case: Path) -> None:
    m = manifest.validate(case.read_bytes(), _files)
    golden = (CORPUS / "normalised" / f"{case.stem}.json").read_bytes().rstrip(b"\n")
    assert manifest.normalised(m) == golden


@pytest.mark.parametrize("case", sorted((CORPUS / "invalid").glob("*.toml")), ids=lambda p: p.stem)
def test_invalid_corpus_raises_its_code(case: Path) -> None:
    first = case.read_text().splitlines()[0]
    code, _, detail = first.removeprefix("# expect: ").strip().partition(" ")
    with pytest.raises(manifest.ManifestInvalid) as exc:
        manifest.validate(case.read_bytes(), _files)
    assert any(e.code == code and (not detail or e.detail == detail) for e in exc.value.errors), (
        exc.value.errors
    )


def test_queue_name_vectors() -> None:
    vectors = ROOT / "shared" / "plugins" / "testdata" / "stage_names.json"
    for v in json.loads(vectors.read_text()):
        assert manifest.queue_name(v["id"], v["stage"]) == v["name"]


def test_the_data_copies_are_the_shared_files() -> None:
    for name in ("manifest.schema.json", "capabilities.toml", "events.toml"):
        assert (DATA / name).read_bytes() == (ROOT / "shared" / "plugins" / name).read_bytes(), name


def test_dollar_does_not_match_before_a_final_newline() -> None:
    src = (
        (CORPUS / "valid" / "minimal.toml")
        .read_text()
        .replace('version = "1.4.0"', 'version = "1.4.0\\n"')
    )
    with pytest.raises(manifest.ManifestInvalid) as exc:
        manifest.validate(src.encode(), _files)
    assert exc.value.has(manifest.CODE_INVALID) or exc.value.has("manifest_bad_version")


def test_package_rules_skipped_without_files() -> None:
    src = (CORPUS / "invalid" / "string_missing_ar.toml").read_bytes()
    manifest.validate(src, None)


def test_dates_are_not_toml_manifests() -> None:
    with pytest.raises(manifest.ManifestInvalid) as exc:
        manifest.validate(b"schema = 1\nwhen = 1979-05-27\n")
    assert exc.value.has(manifest.CODE_NOT_TOML)


def test_a_built_in_manifest_is_read_with_its_trusted_keys_only_when_trusted() -> None:
    """Epic 54, story 54.22: x-trusted-only keys are plugin_trusted_field in any manifest but
    the release's own first-party ones, which must be under tv.maktaba.."""
    root = Path(__file__).resolve().parents[3]
    toy = (
        root / "pipeline" / "tests" / "plugins" / "firstparty" / "toy" / "manifest.toml"
    ).read_bytes()
    pm = manifest
    m = pm.validate(toy, trusted=True)
    assert m["runtime"] == "native-python" and m["trusted"]["runner"] == "light"
    with pytest.raises(pm.ManifestInvalid) as e:
        pm.validate(toy)
    assert e.value.has(pm.CODE_TRUSTED_FIELD)
    with pytest.raises(pm.ManifestInvalid) as e:
        pm.validate(toy.replace(b"tv.maktaba.toy", b"dev.example.toy"), trusted=True)
    assert e.value.has(pm.CODE_RESERVED_ID)
