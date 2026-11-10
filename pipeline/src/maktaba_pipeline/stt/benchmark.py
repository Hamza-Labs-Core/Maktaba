"""How fast this machine turns speech into text (Epic 59 story 59.06).

A NAS's CPU decides whether a library's speech can be transcribed in days or in months, and no
published benchmark gives NAS numbers, so Maktaba measures on the box: the two bundled
30-second clips (``assets/``: one English, one Arabic, public domain) transcribed with this
machine's model, on its device, with its threads and priority. The result is the
**real-time factor**, audio seconds per wall second (2.0: twice as fast as real time; 0.5:
half as fast), and each clip's word error rate against the text it was spoken from.

The server runs it when speech is turned on and after a model change (``[serve]``'s speech
measure), and keeps the answer in ``<state>/stt/speed.json``; Settings → This machine shows it
with the backlog's estimate. CI's ``arm64`` NAS cell runs it to prove the engine works there
and to watch the word error rate per model and architecture.

The machine may be busy with other work while it measures: when the rest of the system kept
the CPU over 80% busy, the run is repeated once, and if it still was the result says
``busy`` (Settings: "Measured while the NAS was busy").

    python -m maktaba_pipeline.stt.benchmark [--model base] [--out FILE] [--clips en,ar]

Exit 0 with the result (JSON) on stdout; 2 when this machine has no speech engine or cannot
load the model (the JSON says why).
"""

from __future__ import annotations

import argparse
import asyncio
import json
import os
import platform
import re
import sys
import time
import unicodedata
from collections.abc import Callable, Sequence
from dataclasses import asdict, dataclass, field
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

__all__ = [
    "ASSETS",
    "BUSY_SHARE",
    "CLIPS",
    "Clip",
    "Result",
    "measure",
    "normalize",
    "word_error_rate",
]

#: the clips and the texts they were spoken from
ASSETS = Path(__file__).with_name("assets")
CLIPS = ("en", "ar")
#: the rest of the system's share of the CPU above which a run is repeated, then marked busy
BUSY_SHARE = 0.8
SCHEMA = 1

# --- the word error rate -----------------------------------------------------------------

#: Arabic's short vowels and other marks (tashkeel), the dagger alif and the tatweel
_ARABIC_MARKS = re.compile("[ؐ-ًؚ-ٰٟۖ-ۭـ]")
_ALEF = str.maketrans({"أ": "ا", "إ": "ا", "آ": "ا", "ٱ": "ا", "ى": "ي", "ة": "ه"})


def normalize(text: str) -> list[str]:
    """The words of ``text`` as the error rate compares them: case folded, punctuation gone,
    Arabic without its marks and with one form of alif, ya and ta marbuta (Whisper writes
    them either way, and a listener hears no difference)."""
    text = unicodedata.normalize("NFKC", text).casefold()
    text = _ARABIC_MARKS.sub("", text).translate(_ALEF)
    text = "".join(" " if unicodedata.category(c)[0] in "PS" else c for c in text)
    return text.split()


def word_error_rate(reference: str, heard: str) -> float:
    """Substitutions, deletions and insertions over the reference's words (0.0 is perfect; it
    can pass 1.0 when far more is heard than was said)."""
    ref, hyp = normalize(reference), normalize(heard)
    if not ref:
        return 0.0 if not hyp else 1.0
    prev = list(range(len(hyp) + 1))
    for i, r in enumerate(ref, 1):
        cur = [i] + [0] * len(hyp)
        for j, h in enumerate(hyp, 1):
            cur[j] = min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + (r != h))
        prev = cur
    return prev[-1] / len(ref)


# --- the measurement ---------------------------------------------------------------------


@dataclass
class Clip:
    lang: str
    audio_sec: float
    wall_sec: float
    #: audio seconds per wall second
    rtf: float
    wer: float
    text: str


@dataclass
class Result:
    schema: int
    model: str
    #: the size name ("base") when the model is one of the tiers' (engine.SIZES)
    size: str | None
    device: str
    compute_type: str
    threads: int
    arch: str
    measured_at: str
    #: every clip's audio over every clip's wall time
    rtf: float
    #: the rest of the system kept the CPU over BUSY_SHARE busy (after one repeat)
    busy: bool
    repeated: bool
    clips: list[Clip] = field(default_factory=list)
    version: str = ""

    def as_json(self) -> dict[str, Any]:
        return asdict(self)


#: transcribes one clip: (path, language) -> (text, audio seconds)
Transcribe = Callable[[Path, str], tuple[str, float]]


def _cpu_now() -> tuple[float, float, int]:
    """(the system's busy CPU seconds, this process's CPU seconds, the CPUs)."""
    import psutil

    t = psutil.cpu_times()
    busy = sum(getattr(t, f, 0.0) for f in ("user", "nice", "system", "irq", "softirq", "steal"))
    me = psutil.Process().cpu_times()
    return busy, me.user + me.system, psutil.cpu_count() or 1


def _run(clips: Sequence[str], transcribe: Transcribe, assets: Path) -> tuple[list[Clip], float]:
    """Each clip transcribed once, and the share of the CPU the rest of the system took."""
    out: list[Clip] = []
    sys0, me0, cpus = _cpu_now()
    w0 = time.monotonic()
    for lang in clips:
        ref = (assets / f"{lang}.txt").read_text(encoding="utf-8")
        t0 = time.monotonic()
        text, audio = transcribe(assets / f"{lang}.ogg", lang)
        wall = max(time.monotonic() - t0, 1e-6)
        out.append(
            Clip(
                lang=lang,
                audio_sec=round(audio, 2),
                wall_sec=round(wall, 2),
                rtf=round(audio / wall, 3),
                wer=round(word_error_rate(ref, text), 4),
                text=text.strip(),
            )
        )
    wall = max(time.monotonic() - w0, 1e-6)
    sys1, me1, _ = _cpu_now()
    others = max(0.0, (sys1 - sys0) - (me1 - me0)) / (wall * cpus)
    return out, others


def measure(
    transcribe: Transcribe,
    *,
    model: str,
    device: str = "cpu",
    compute_type: str = "int8",
    threads: int = 0,
    clips: Sequence[str] = CLIPS,
    assets: Path = ASSETS,
    version: str = "",
) -> Result:
    """Transcribe the clips with ``transcribe``, again once when the rest of the system kept
    the CPU over :data:`BUSY_SHARE` busy, and say how fast it went."""
    from . import engine

    got, others = _run(clips, transcribe, assets)
    repeated = False
    if others > BUSY_SHARE:
        repeated = True
        got, others = _run(clips, transcribe, assets)
    audio = sum(c.audio_sec for c in got)
    wall = sum(c.wall_sec for c in got)
    return Result(
        schema=SCHEMA,
        model=model,
        size=engine.size_of(model),
        device=device,
        compute_type=compute_type,
        threads=threads,
        arch=platform.machine().lower(),
        measured_at=datetime.now(UTC).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        rtf=round(audio / wall, 3) if wall > 0 else 0.0,
        busy=others > BUSY_SHARE,
        repeated=repeated,
        clips=got,
        version=version,
    )


def threads_for(cores: int) -> int:
    """One CTranslate2 thread per two cores, at least one (story 59.06: a NAS keeps cores for
    serving files)."""
    return max(1, cores // 2)


def _engine(model: str, threads: int) -> tuple[Transcribe, str, str, str]:
    """faster-whisper on this machine's model (fetched and checked as a worker fetches it),
    on the CPU or the GPU the settings name: the transcribe function, the device, the
    precision and the versions."""
    from importlib.metadata import version

    import ctranslate2  # type: ignore[import-untyped,import-not-found,unused-ignore]
    from faster_whisper import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        WhisperModel,
    )

    from . import ct2
    from .env import decode_audio, settings_from_env

    settings = settings_from_env()
    ct2.be_polite()
    folder = ct2.model_dir(model)
    device, index = ct2.choose_device(settings.device)
    compute = ct2.choose_compute_type(
        device, settings.compute_type, list(ctranslate2.get_supported_compute_types(device, index))
    )
    fw = WhisperModel(
        folder,
        device=device,
        device_index=index,
        compute_type=compute,
        cpu_threads=threads if device == "cpu" else 0,
        num_workers=1,
    )

    def transcribe(path: Path, lang: str) -> tuple[str, float]:
        # ffmpeg, as a worker decodes a file (faster-whisper's own PyAV decoder is not used)
        audio = asyncio.run(decode_audio(str(path)))
        segments, _info = fw.transcribe(audio, language=lang, word_timestamps=True)
        return " ".join(s.text.strip() for s in segments), len(audio) / 16_000

    versions = f"faster-whisper {version('faster-whisper')}, ctranslate2 {ctranslate2.__version__}"
    return transcribe, device, compute, versions


def _missing_engine() -> str | None:
    """Why this Python has no speech engine (the stt extra: NumPy, CTranslate2, faster-whisper),
    asked before anything imports them, or None."""
    import importlib.util

    for module, package in (
        ("numpy", "numpy"),
        ("ctranslate2", "ctranslate2"),
        ("faster_whisper", "faster-whisper"),
    ):
        if importlib.util.find_spec(module) is None:
            return f"{package} is not installed (the stt extra)"
    return None


def _write(path: Path, doc: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + ".tmp")
    tmp.write_text(json.dumps(doc, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    os.replace(tmp, path)


def main(argv: Sequence[str] | None = None) -> int:
    from . import engine

    ap = argparse.ArgumentParser(prog="python -m maktaba_pipeline.stt.benchmark")
    ap.add_argument("--model", help="a size (tiny, base, small, medium, large-v3-turbo) or a repo;"
                    " default: MAKTABA_STT_MODEL, as a worker takes it")  # fmt: skip
    ap.add_argument("--threads", type=int, default=0,
                    help="CTranslate2's CPU threads (default: MAKTABA_STT_CPU_THREADS,"
                    " else half the cores)")  # fmt: skip
    ap.add_argument("--clips", default=",".join(CLIPS), help="the clips: en, ar")
    ap.add_argument("--out", type=Path, help="also write the result here (atomically)")
    args = ap.parse_args(argv)

    model = engine.model_for(engine.CT2, args.model or os.environ.get("MAKTABA_STT_MODEL"))
    threads = args.threads or int(os.environ.get("MAKTABA_STT_CPU_THREADS") or 0)
    threads = threads or threads_for(os.cpu_count() or 2)
    clips = [c.strip() for c in args.clips.split(",") if c.strip() in CLIPS]
    if engine.current() != engine.CT2:
        problem: str | None = (
            "the speech measure runs on the CTranslate2 engine (Windows and Linux)"
        )
    else:
        problem = _missing_engine()
    if problem is None:
        from . import ct2
        from .env import settings_from_env

        problem = ct2.unavailable(settings_from_env())
    if problem is None:
        try:
            transcribe, device, compute, versions = _engine(model, threads)
        except Exception as exc:  # the model did not download or load: said, not raised
            problem = f"{type(exc).__name__}: {exc}"[:500]
    if problem is not None:
        doc: dict[str, Any] = {"schema": SCHEMA, "model": model, "error": problem}
        if args.out:
            _write(args.out, doc)
        sys.stdout.write(json.dumps(doc, ensure_ascii=False) + "\n")
        return 2
    result = measure(
        transcribe,
        model=model,
        device=device,
        compute_type=compute,
        threads=threads if device == "cpu" else 0,
        clips=clips,
        version=versions,
    )
    doc = result.as_json()
    if args.out:
        _write(args.out, doc)
    sys.stdout.write(json.dumps(doc, ensure_ascii=False) + "\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
