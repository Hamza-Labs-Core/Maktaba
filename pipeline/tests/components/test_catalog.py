"""The component catalog against the code (story 51.01): every stage of the graph and of
the built-in extensions is in exactly one component, the pipeline's copy is the shared
file, and this loader answers what the Go loader answers."""

from __future__ import annotations

import ast
import importlib
import importlib.resources
from pathlib import Path
from typing import Any

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


# Every StageDef the package's source can make, found by reading every module of it (not
# a list of files, nor only what GRAPH and BUILTIN import): a stage added in a new file, or
# by an extension left out of BUILTIN, cannot escape the check above.
_PACKAGE = _ROOT / "pipeline" / "src" / "maktaba_pipeline"


def _stage_factories(tree: ast.Module) -> dict[str, int]:
    """The module's functions that return a StageDef named by one of their parameters
    (``engine/graph.py``'s ``_legacy``): function name -> that parameter's position."""
    out: dict[str, int] = {}
    for fn in ast.walk(tree):
        if not isinstance(fn, ast.FunctionDef | ast.AsyncFunctionDef):
            continue
        params = [a.arg for a in fn.args.posonlyargs + fn.args.args]
        for call in ast.walk(fn):
            name = _stage_name_arg(call)
            if isinstance(name, ast.Name) and name.id in params:
                out[fn.name] = params.index(name.id)
    return out


def _callee(node: ast.AST) -> str | None:
    if not isinstance(node, ast.Call):
        return None
    f = node.func
    return f.id if isinstance(f, ast.Name) else f.attr if isinstance(f, ast.Attribute) else None


def _stage_name_arg(node: ast.AST, position: int = 0) -> ast.expr | None:
    """The ``name`` argument of a ``StageDef(...)`` call (keyword or first positional)."""
    if _callee(node) != "StageDef":
        return None
    assert isinstance(node, ast.Call)
    for kw in node.keywords:
        if kw.arg == "name":
            return kw.value
    return node.args[position] if len(node.args) > position else None


def source_stages(root: Path = _PACKAGE) -> tuple[dict[str, str], list[str]]:
    """Every stage name a StageDef call (or a factory of one) in ``root``'s modules makes,
    with where; and every call whose name is not a string literal (which no static check
    can map, so it is refused)."""
    found: dict[str, str] = {}
    bad: list[str] = []
    for path in sorted(root.rglob("*.py")):
        src = path.read_text(encoding="utf-8")
        if "StageDef(" not in src:
            continue
        tree = ast.parse(src, filename=str(path))
        factories = _stage_factories(tree)
        inside = {
            id(n)
            for fn in ast.walk(tree)
            if isinstance(fn, ast.FunctionDef | ast.AsyncFunctionDef) and fn.name in factories
            for n in ast.walk(fn)
        }
        for node in ast.walk(tree):
            callee = _callee(node)
            if callee == "StageDef":
                if id(node) in inside:
                    continue  # the factory's own call: its callers name the stage
                arg = _stage_name_arg(node)
            elif callee in factories:
                assert isinstance(node, ast.Call)
                pos = factories[callee]
                arg = node.args[pos] if len(node.args) > pos else None
                for kw in node.keywords:
                    if kw.arg == "name":
                        arg = kw.value
            else:
                continue
            where = f"{path.relative_to(root.parent)}:{getattr(node, 'lineno', 0)}"
            if isinstance(arg, ast.Constant) and isinstance(arg.value, str):
                found.setdefault(arg.value, where)
            else:
                bad.append(where)
    return found, bad


def test_every_stage_in_the_source_is_in_the_catalog_and_the_graph() -> None:
    found, bad = source_stages()
    assert not bad, f"StageDef named by no string literal (the catalog check cannot map it): {bad}"
    assert len(found) >= 40, f"found only {len(found)} stages in {_PACKAGE}"
    cat = catalog.default()
    missing = {s: w for s, w in found.items() if cat.by_stage(s) is None}
    assert not missing, f"stages in no catalog component: {missing} (add them to catalog.toml)"
    # and every one is in what the pipeline runs (GRAPH and BUILTIN): a stage made in a
    # file nothing loads is dead code, or an extension left out of BUILTIN
    loaded = {s.name for s in _all_stages()}
    unloaded = {s: w for s, w in found.items() if s not in loaded}
    assert not unloaded, f"stages no graph the pipeline builds has: {unloaded}"
    assert loaded <= set(found), sorted(loaded - set(found))


def test_a_stage_in_a_new_file_is_found(tmp_path: Path) -> None:
    pkg = tmp_path / "pkg"
    (pkg / "extensions" / "newext").mkdir(parents=True)
    (pkg / "extensions" / "newext" / "__init__.py").write_text(
        "from x import StageDef\n"
        "def register(g):\n"
        "    g.add_stage(StageDef(name='brand_new_stage', label='x'))\n"
        "def _make(stage_name, label):\n"
        "    return StageDef(name=stage_name, label=label)\n"
        "LATE = (_make('made_by_a_factory', 'y'), StageDef('positional', 'z'))\n"
        "def dynamic(n):\n"
        "    return StageDef(name=f'stage_{n}', label='w')\n",
        encoding="utf-8",
    )
    found, bad = source_stages(pkg)
    assert set(found) == {"brand_new_stage", "made_by_a_factory", "positional"}
    assert bad == ["pkg/extensions/newext/__init__.py:8"]
    assert unmapped(catalog.default(), sorted(found)) == sorted(found)


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


def _check_copy() -> None:
    shared = (_SHARED / "catalog.toml").read_bytes()
    # a plain raise, not assert: pytest would diff the two files line by line (untruncated
    # under CI=true), which takes over a second and trips the unit soft cap
    if catalog.raw() != shared:
        raise AssertionError("the pipeline's catalog.toml differs: run make catalog-sync")


# T05: the packaged copy is the shared file, byte for byte.
def test_pipeline_copy_is_the_shared_file() -> None:
    _check_copy()


class _Package:
    """A stand-in for ``importlib.resources.files(<package>)``: the package folder."""

    def __init__(self, root: Path) -> None:
        self.root = root

    def joinpath(self, name: str) -> Path:
        return self.root / name


@pytest.mark.parametrize(
    "edit",
    [
        lambda b: b + b"\n# edited\n",
        lambda b: b.replace(b"drain     = { grace_s = 120,", b"drain     = { grace_s = 90,", 1),
    ],
    ids=["comment", "drain"],
)
def test_an_edited_copy_is_caught(
    edit: Any, tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    # the packaged file the pipeline loads, edited: the check above refuses it, and what
    # the pipeline would run (raw, default) is that edited file, not the shared one
    shared = (_SHARED / "catalog.toml").read_bytes()
    edited = edit(shared)
    assert edited != shared
    (tmp_path / "catalog.toml").write_bytes(edited)
    pkg = catalog.__package__
    real_files = importlib.resources.files
    monkeypatch.setattr(
        importlib.resources,
        "files",
        lambda p: _Package(tmp_path) if p == pkg else real_files(p),
    )
    assert catalog.raw() == edited
    with pytest.raises(AssertionError, match="run make catalog-sync"):
        _check_copy()
    catalog.default.cache_clear()
    try:
        grace = catalog.default().get("transcription")
        assert grace is not None
        assert grace.drain.grace_s == (90 if b"grace_s = 90," in edited else 120)
    finally:
        catalog.default.cache_clear()


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
