"""The speech measure (Epic 59 story 59.06): the clips, the word error rate, the busy repeat."""

from __future__ import annotations

import json
import tomllib
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.stt import benchmark, engine


def test_the_clips_are_bundled_with_their_texts() -> None:
    for lang in benchmark.CLIPS:
        clip = benchmark.ASSETS / f"{lang}.ogg"
        assert clip.is_file() and 50_000 < clip.stat().st_size < 300_000, clip
        assert len(benchmark.normalize((benchmark.ASSETS / f"{lang}.txt").read_text("utf-8"))) > 40
    assert "Public Domain Mark" in (benchmark.ASSETS / "README.md").read_text("utf-8")


def test_the_word_error_rate() -> None:
    wer = benchmark.word_error_rate
    assert wer("The cat sat.", "the cat sat") == 0.0
    assert wer("the cat sat", "the dog sat") == pytest.approx(1 / 3)
    assert wer("the cat sat", "the cat") == pytest.approx(1 / 3)
    assert wer("the cat", "the cat sat down") == pytest.approx(1.0)
    # Arabic: marks, alif forms, alif maqsura and ta marbuta do not count
    assert wer("إلى المدرسة الكبيرة", "الى المدرسه الكبيره") == 0.0
    assert wer("كَتَبَ الولدُ", "كتب الولد") == 0.0
    assert wer("في السوق", "في البيت") == pytest.approx(0.5)
    assert wer("", "") == 0.0


def _fake(text_of: dict[str, str], seconds: float = 30.0) -> benchmark.Transcribe:
    def t(path: Path, lang: str) -> tuple[str, float]:
        return text_of.get(lang, ""), seconds

    return t


def test_a_measure_reports_speed_and_errors(monkeypatch: pytest.MonkeyPatch) -> None:
    clock = iter([0.0, 0.0, 10.0, 10.0, 70.0, 70.0])  # en takes 10 s, ar 60 s
    monkeypatch.setattr(benchmark.time, "monotonic", lambda: next(clock))
    monkeypatch.setattr(benchmark, "_cpu_now", lambda: (0.0, 0.0, 4))
    ref = {lang: (benchmark.ASSETS / f"{lang}.txt").read_text("utf-8") for lang in benchmark.CLIPS}
    r = benchmark.measure(_fake(ref), model=engine.SIZES["base"], threads=2)
    assert r.size == "base" and r.threads == 2 and not r.busy and not r.repeated
    en, ar = r.clips
    assert (en.rtf, ar.rtf) == (3.0, 0.5) and en.wer == 0.0 and ar.wer == 0.0
    assert r.rtf == pytest.approx(60 / 70, abs=1e-3)
    doc = r.as_json()
    assert json.loads(json.dumps(doc))["clips"][1]["lang"] == "ar"


def test_a_busy_machine_is_measured_again_then_said(monkeypatch: pytest.MonkeyPatch) -> None:
    t = [0.0]

    def tick() -> float:
        t[0] += 1.0
        return t[0]

    monkeypatch.setattr(benchmark.time, "monotonic", tick)
    # the rest of the system takes 3.6 of 4 CPUs (90%) each run
    busy = iter([(0.0, 0.0, 4), (1000.0, 0.0, 4), (0.0, 0.0, 4), (1000.0, 0.0, 4)])
    monkeypatch.setattr(benchmark, "_cpu_now", lambda: next(busy))
    r = benchmark.measure(_fake({}), model="x", clips=["en"])
    assert r.repeated and r.busy and r.clips[0].wer == 1.0
    quiet = iter([(0.0, 0.0, 4), (1000.0, 0.0, 4), (0.0, 0.0, 4), (0.0, 0.0, 4)])
    monkeypatch.setattr(benchmark, "_cpu_now", lambda: next(quiet))
    r = benchmark.measure(_fake({}), model="x", clips=["en"])
    assert r.repeated and not r.busy


def test_threads_are_one_per_two_cores() -> None:
    assert [benchmark.threads_for(n) for n in (1, 2, 3, 4, 8)] == [1, 1, 1, 2, 4]


def test_without_an_engine_it_says_why(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: Any
) -> None:
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)
    out = tmp_path / "stt" / "speed.json"
    assert benchmark.main(["--model", "base", "--out", str(out)]) == 2
    doc = json.loads(out.read_text("utf-8"))
    assert doc["model"] == engine.SIZES["base"] and "CTranslate2" in doc["error"]
    assert json.loads(capsys.readouterr().out) == doc


def test_the_sizes_are_the_catalogs_models() -> None:
    """engine.SIZES and its pins are the catalog's model.whisper-* packs, at the same commits
    and hashes (tools/release/cmd/pin-models pins the catalog)."""
    catalog = Path(engine.__file__).parents[1] / "components" / "catalog.toml"
    comps = {c["id"]: c for c in tomllib.loads(catalog.read_text("utf-8"))["component"]}
    for size in ("tiny", "base", "small", "medium"):
        comp = comps[f"model.whisper-{size}"]
        repo, _, rev = comp["source"].removeprefix("hf:").partition("@")
        assert engine.SIZES[size] == repo
        pin = engine.CT2_PINS[repo]
        assert pin.revision == rev
        files = {f["path"]: f["sha256"] for f in comp["files"] if "source" not in f}
        assert dict(pin.sha256) == files, size
    assert engine.model_for(engine.CT2, "small") == "Systran/faster-whisper-small"
    assert engine.model_for(engine.CT2, "large-v3-turbo") == engine.DEFAULT_MODEL[engine.CT2]
    assert engine.size_of("Systran/faster-whisper-base") == "base" and engine.size_of("x/y") is None


def test_a_transcribing_process_is_polite(monkeypatch: pytest.MonkeyPatch) -> None:
    """Story 59.06: MAKTABA_STT_NICE lowers the priority once, never raises it."""
    import os

    from maktaba_pipeline.stt import ct2

    if not hasattr(os, "getpriority"):
        pytest.skip("no process priorities here")
    now = os.getpriority(os.PRIO_PROCESS, 0)
    set_to: list[int] = []
    monkeypatch.setattr(os, "setpriority", lambda which, who, v: set_to.append(v))
    monkeypatch.delenv("MAKTABA_STT_NICE", raising=False)
    assert ct2.be_polite() is None and set_to == []
    monkeypatch.setenv("MAKTABA_STT_NICE", str(now + 1))
    assert ct2.be_polite() == now + 1 and set_to == [now + 1]
    monkeypatch.setenv("MAKTABA_STT_NICE", "0")
    set_to.clear()
    assert ct2.be_polite() == now and set_to == []  # never raised back
