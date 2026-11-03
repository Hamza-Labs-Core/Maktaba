"""The component catalog against the code (story 51.01): every stage of the graph and of
the built-in extensions is in exactly one component, the pipeline's copy is the shared
file, and this loader answers what the Go loader answers."""

from __future__ import annotations

import importlib
from pathlib import Path

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.components import catalog
from maktaba_pipeline.engine.graph import GRAPH, GraphBuilder, StageDef
from maktaba_pipeline.extensions import BUILTIN

pytestmark = pytest.mark.unit

_ROOT = Path(__file__).resolve().parents[3]
_SHARED = _ROOT / "shared" / "components"


def _builtin_stages() -> dict[str, str]:
    """Each stage a built-in extension adds, with its extension's name."""
    out: dict[str, str] = {}
    for target in BUILTIN:
        module, _, attr = target.partition(":")
        ext = getattr(importlib.import_module(module), attr)
        ext = ext if hasattr(ext, "manifest") else ext()
        g = GraphBuilder(GRAPH)
        ext.register(g)
        for s in g.build():
            if s.name not in {x.name for x in GRAPH}:
                out[s.name] = ext.manifest.name
    return out


def _all_stages() -> list[StageDef]:
    g = GraphBuilder(GRAPH)
    for target in BUILTIN:
        module, _, attr = target.partition(":")
        ext = getattr(importlib.import_module(module), attr)
        (ext if hasattr(ext, "manifest") else ext()).register(g)
    return list(g.build())


def unmapped(cat: catalog.Catalog, stages: list[str]) -> list[str]:
    return [s for s in stages if cat.by_stage(s) is None]


# T04: every StageDef in the graph and the built-in extensions is in exactly one
# component (the loader refuses a stage in two); no catalog stage is missing from them.
def test_every_stage_is_in_exactly_one_component() -> None:
    cat = catalog.default()
    names = [s.name for s in _all_stages()]
    missing = unmapped(cat, names)
    assert not missing, f"stages in no catalog component: {missing} (add them to catalog.toml)"
    listed = [s for c in cat.components() for s in c.stages]
    assert len(listed) == len(set(listed))
    extra = sorted(set(listed) - set(names))
    assert not extra, f"catalog stages the graph does not have: {extra}"


def test_extension_stages_belong_to_their_extension_component() -> None:
    cat = catalog.default()
    for stage, ext in _builtin_stages().items():
        c = cat.by_stage(stage)
        assert c is not None and c.id == f"ext.{ext}", (stage, ext, c)


def test_a_new_stage_is_named() -> None:
    cat = catalog.default()
    assert unmapped(cat, ["transcribe", "brand_new_stage"]) == ["brand_new_stage"]


def test_every_component_placement_agrees_with_its_stages() -> None:
    """A component with stages is home-only exactly when every one of its stages is."""
    cat = catalog.default()
    by_name = {s.name: s for s in _all_stages()}
    for c in cat.components():
        if c.stages:
            home = all(by_name[s].placement == "home" for s in c.stages)
            assert (c.placement == "home") == home, (c.id, c.placement)


def test_rules_sections_exist() -> None:
    cat = catalog.default()
    owned: dict[str, str] = {}
    for c in cat.components():
        for s in c.settings:
            if s.startswith("rules."):
                section = s.removeprefix("rules.")
                assert section in rules.SECTIONS, f"{c.id}: no rules section {section!r}"
                assert section not in owned, f"rules.{section} in {owned[section]} and {c.id}"
                owned[section] = c.id


# T05: the packaged copy is the shared file, byte for byte.
def test_pipeline_copy_is_the_shared_file() -> None:
    shared = (_SHARED / "catalog.toml").read_bytes()
    assert catalog.raw() == shared, "the pipeline's catalog.toml differs: run make catalog-sync"


def test_an_edited_copy_is_caught() -> None:
    shared = (_SHARED / "catalog.toml").read_bytes()
    assert shared + b"\n# edited\n" != shared  # what the check above compares


# T09 (Python side): the dump is the fixture the Go loader writes.
def test_dump_matches_the_go_loader() -> None:
    want = (_SHARED / "testdata" / "catalog.dump.json").read_text(encoding="utf-8")
    assert catalog.default().dump() == want


def _fixture(name: str) -> str:
    return (_SHARED / "testdata" / name).read_text(encoding="utf-8")


@pytest.mark.parametrize(
    ("name", "want"),
    [
        ("cycle.toml", "requires cycle: a -> b -> a"),
        ("unknown_field.toml", "unknown field 'stagez'"),
        ("core_switchable.toml", "core.* components must have switchable = false"),
        ("conflicts_one_side.toml", "conflicts must be listed on both sides"),
        ("stage_twice.toml", "stage 'thumbnail' is in both a and b"),
    ],
)
def test_bad_catalogs_are_refused(name: str, want: str) -> None:
    with pytest.raises(catalog.CatalogError) as e:
        catalog.load(_fixture(name))
    assert want in str(e.value)


@pytest.mark.parametrize(
    ("old", "new", "want"),
    [
        ('kind      = "loop"', 'kind      = "loop+stages"', "bad kind"),
        ('kind      = "loop"', 'kind      = "stages+loop"', "but no stages"),
        ('group     = "system"', 'group     = "nope"', "bad group"),
        ('platforms = ["linux-amd64"]', 'platforms = ["beos"]', "bad platforms entry"),
        ('since     = "0.3.66"', 'since = "0.3.66"\nrequires = ["zz"]', "requires unknown"),
        ('loops     = ["api:a"]', 'loops     = ["web:a"]', "bad loops entry"),
        ('kind      = "loop"', 'kind      = "asset"', "an asset has no loops"),
        ('since     = "0.3.66"', 'since = "0.3.66"\nfeatures = ["nope"]', "bad features"),
        ('"failed"]', '"failed", "zz"]', "phases must be exactly"),
    ],
)
def test_field_rules(old: str, new: str, want: str) -> None:
    base = _fixture("conflicts_one_side.toml").replace('conflicts = ["b"]', "", 1)
    catalog.load(base)
    src = base.replace(old, new, 1)
    assert src != base
    with pytest.raises(catalog.CatalogError) as e:
        catalog.load(src)
    assert want in str(e.value)


def test_accessors() -> None:
    cat = catalog.default()
    tr = cat.by_process("pipeline", "stt-4")
    assert tr is not None and tr.id == "transcription"
    main = cat.by_process("pipeline")
    assert main is not None and main.id == "core.pipeline"
    loop = cat.by_loop("pipeline", "channel-topup")
    assert loop is not None and loop.id == "livetv"
    assert cat.requires("transcription")[1] == (
        "model.whisper-turbo",
        "model.whisper-large-v3",
        "model.whisper-medium",
    )
    order = cat.order()
    assert order.index("runtime.ml") < order.index("transcription")
    assert {name for name in cat.loops_of("pipeline")} >= {"queue-health", "channel-topup"}
    tc = cat.get("transcription")
    assert tc is not None and tc.drain.grace_s == 120 and tc.drain.checkpoint
    assert cat.is_reason("held") and not cat.is_reason("nonsense")


_TURBO_MLX = "hf:mlx-community/whisper-large-v3-turbo@"
_MEDIUM = 'source     = "hf:Systran/faster-whisper-medium@08e178d48790749d25932bbc082711ddcfdfbc4f"'


@pytest.mark.parametrize(
    ("old", "new", "want"),
    [
        (
            _MEDIUM,
            'source     = "hf:Systran/faster-whisper-medium@main"',
            "component model.whisper-medium: source"
            " 'hf:Systran/faster-whisper-medium@main' must be pinned",
        ),
        (_MEDIUM, 'source     = "hf:Systran/faster-whisper-medium"', "must be pinned"),
        (
            f'darwin-arm64 = "{_TURBO_MLX}a4aaeec0636e6fef84abdcbe3544cb2bf7e9f6fb" }}',
            f'darwin-arm64 = "{_TURBO_MLX}{"1" * 40}" }}',
            "has no files",
        ),
        ('"ollama:qwen2.5:7b@sha256:', '"ollama:qwen2.5:7b@latest', "must be pinned as ollama"),
    ],
)
def test_model_sources_are_pinned(old: str, new: str, want: str) -> None:
    """Story 51.15 T11: a model is pinned to a commit (or digest), with its files."""
    raw = catalog.raw().decode()
    src = raw.replace(old, new, 1)
    assert src != raw
    with pytest.raises(catalog.CatalogError) as e:
        catalog.load(src)
    assert want in str(e.value)


def test_pinned_models_have_their_files() -> None:
    cat = catalog.default()
    turbo = cat.get("model.whisper-turbo")
    assert turbo is not None
    assert turbo.files_of(turbo.source) and turbo.files_of(turbo.source_for("darwin-arm64"))
    cohere = cat.get("model.cohere-arabic")
    assert cohere is not None and cohere.gated
