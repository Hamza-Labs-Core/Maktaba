"""``maktaba-pipeline models status|download``: the model files the workers load (story 36.4).

::

    models status   [--only NAMES] [--json]     what is here already (no network)
    models download [--only NAMES] [--share DIR] [--json]
                                                 fetch what is missing, with progress

The items are exactly what the workers load, from the same settings:

``whisper``     ``MAKTABA_STT_MODEL`` in this machine's engine (:mod:`~..stt.engine`): on
                Apple Silicon ``mlx-community/whisper-large-v3-turbo``, the whole repo, as
                ``mlx_whisper.load_models`` downloads it; elsewhere the CTranslate2
                ``dropbox-dash/faster-whisper-large-v3-turbo``, the files the engine loads
                (:data:`~..stt.engine.CT2_FILES`)
``cohere``      the second listen, ``MAKTABA_STT_REFINE_MODEL`` (gated: the account
                must have accepted its licence; ``HF_TOKEN`` or ``hf auth login``), with
                mlx-audio's file patterns on Apple Silicon, the files transformers loads
                elsewhere (:data:`~..stt.cohere_cuda.ALLOW_PATTERNS`); skipped with
                ``MAKTABA_STT_REFINE=0``, and on a machine that cannot run it (Windows,
                Linux: no NVIDIA GPU, a torch without CUDA, transcription on the CPU, or a
                GPU too small to hold it beside Whisper, :func:`~..stt.cohere.unavailable`),
                saying why. When Hugging Face refuses, the home's published copy on
                ``--share`` (default ``$MAKTABA_MODELS_DIR``) is copied here instead
                (:mod:`~..stt.model_share`)
``dictionary``  CAMeL Tools' ``morphology-db-msa-r13`` (the second listen's spelling;
                skipped with it)
``embedder``    ``MAKTABA_EMBED_MODEL`` (default ``intfloat/multilingual-e5-small``),
                for semantic search

Files land in the Hugging Face cache (``HF_HOME``) and ``CAMELTOOLS_DATA``, which
``MAKTABA_MODEL_CACHE_DIR`` places (:mod:`~..model_dirs`). With ``--json`` every change
is one JSON object per line on stdout (``event``: ``model`` | ``done``); exit 0 when
every item is ready, 3 when only the gated model is missing for want of a Hugging Face
sign-in, 1 on any other failure; ``status`` exits 10 when something is missing.
The contract: ``specs/epics/36-mac-app/contract-venv.md``.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import sys
import time
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

__all__ = ["EXIT_NEEDS_LOGIN", "Item", "add_parser", "items", "main"]

EXIT_OK = 0
EXIT_FAILED = 1
EXIT_NEEDS_LOGIN = 3
EXIT_MISSING = 10  #: status only: something is not here yet

NAMES = ("whisper", "cohere", "dictionary", "embedder")
#: written into a Hugging Face snapshot once ``download`` fetched all of it: the cache's
#: own ``refs/`` is written before the first file, so a download cut short looks whole
COMPLETE = ".maktaba-complete"
CAMEL_PACKAGE = "morphology-db-msa-r13"

#: what sentence-transformers never reads (other frameworks' weights)
_EMBEDDER_IGNORE = ["*.onnx", "onnx/*", "openvino/*", "*.msgpack", "*.h5", "*.ot", "*.tflite"]


@dataclass
class Item:
    name: str
    model: str  #: a Hugging Face repo id, or the CAMeL package
    kind: str  #: ``hf`` | ``camel``
    gated: bool = False
    allow_patterns: list[str] | None = None
    ignore_patterns: list[str] | None = None
    #: why it is not needed (set: reported as skipped)
    skip: str | None = None
    state: str = "pending"
    source: str = ""
    path: str = ""
    message: str = ""
    done_bytes: int = 0
    total_bytes: int = 0
    extra: dict[str, Any] = field(default_factory=dict)

    def event(self) -> dict[str, Any]:
        out: dict[str, Any] = {
            "event": "model",
            "name": self.name,
            "model": self.model,
            "state": self.state,
        }
        for key in ("source", "path", "message"):
            if getattr(self, key):
                out[key] = getattr(self, key)
        if self.total_bytes:
            out["bytes"] = self.done_bytes
            out["total_bytes"] = self.total_bytes
        return out


def _cohere_patterns() -> list[str] | None:
    """What the second listen loads: on CUDA the files transformers reads; on Apple
    Silicon mlx-audio's own download patterns (what its loader fetches), when installed."""
    from ..stt import engine

    if engine.current() != engine.MLX:
        from ..stt.cohere_cuda import ALLOW_PATTERNS

        return list(ALLOW_PATTERNS)
    if importlib.util.find_spec("mlx_audio") is None:
        return None
    try:
        from mlx_audio.utils import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
            DEFAULT_ALLOW_PATTERNS,
        )
    except Exception:  # noqa: BLE001 - a different mlx-audio: the whole repo
        return None
    return list(DEFAULT_ALLOW_PATTERNS)


def items(only: set[str] | None = None) -> list[Item]:
    """The models this machine's workers load, from the same settings they read."""
    from ..engine.stages.transcribe import refine_model, settings_from_env
    from ..search.semantic import embed_model
    from ..stt import cohere, engine

    whisper = settings_from_env().model
    refine = refine_model()
    no_refine = None if refine else "the second listen is off (MAKTABA_STT_REFINE=0)"
    if no_refine is None and (not only or only & {"cohere", "dictionary"}):
        # (on Windows and Linux this imports torch and asks CUDA: only when asked about)
        no_refine = cohere.unavailable()
    ct2 = engine.current() == engine.CT2
    out = [
        Item(
            "whisper",
            engine.ct2_repo(whisper) if ct2 else whisper,
            "hf",
            allow_patterns=list(engine.CT2_FILES) if ct2 else None,
        ),
        Item(
            "cohere",
            refine or "",
            "hf",
            gated=True,
            allow_patterns=_cohere_patterns(),
            skip=no_refine,
        ),
        Item("dictionary", CAMEL_PACKAGE, "camel", skip=no_refine),
        Item("embedder", embed_model(), "hf", ignore_patterns=_EMBEDDER_IGNORE),
    ]
    if Path(whisper).expanduser().is_dir():
        out[0].skip = f"a local folder ({whisper})"
    if only:
        out = [i for i in out if i.name in only]
    return out


# --- the downloaders (module level: tests stand in for them) ------------------------------


def _bar_class(on_bytes: Callable[[int, int], None]) -> type[Any]:
    """A tqdm that draws nothing and reports snapshot_download's byte total."""
    from tqdm.std import tqdm  # type: ignore[import-untyped,unused-ignore]

    class _Null:
        def write(self, _s: str) -> None:
            return None

        def flush(self) -> None:
            return None

    class _Bar(tqdm):  # type: ignore[misc,type-arg,unused-ignore]
        def __init__(self, *args: Any, **kwargs: Any) -> None:
            kwargs.pop("name", None)
            kwargs["file"] = _Null()
            kwargs["disable"] = False
            self._bytes = kwargs.get("unit") == "B"
            super().__init__(*args, **kwargs)

        def update(self, n: float | None = 1) -> bool | None:
            r = super().update(n)
            if self._bytes:
                on_bytes(int(self.n), int(self.total or 0))
            return r  # type: ignore[no-any-return]

    return _Bar


def hf_download(item: Item, on_bytes: Callable[[int, int], None], local_only: bool) -> Path:
    from huggingface_hub import snapshot_download

    return Path(
        snapshot_download(
            item.model,
            allow_patterns=item.allow_patterns,
            ignore_patterns=item.ignore_patterns,
            local_files_only=local_only,
            tqdm_class=None if local_only else _bar_class(on_bytes),
        )
    )


def camel_installed() -> bool:
    if importlib.util.find_spec("camel_tools") is None:
        return False
    from ..model_dirs import ensure_camel_data

    ensure_camel_data()
    from camel_tools.data.catalogue import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        CT_VERSIONS_PATH,
    )

    try:
        return CAMEL_PACKAGE in json.loads(Path(CT_VERSIONS_PATH).read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return False


def camel_download() -> None:
    if importlib.util.find_spec("camel_tools") is None:
        raise RuntimeError("CAMeL Tools is not installed (the stt extra)")
    from ..model_dirs import ensure_camel_data

    ensure_camel_data()
    from camel_tools.data import (  # type: ignore[import-untyped,import-not-found,unused-ignore]
        CATALOGUE,
    )

    CATALOGUE.download_package(CAMEL_PACKAGE)


def needs_login(exc: BaseException) -> bool:
    """Hugging Face refused for want of a sign-in or an accepted licence."""
    try:
        from huggingface_hub.errors import GatedRepoError, RepositoryNotFoundError
    except ImportError:
        return False
    if isinstance(exc, GatedRepoError | RepositoryNotFoundError):
        return True
    status = getattr(getattr(exc, "response", None), "status_code", None)
    return status in (401, 403)


# --- running -----------------------------------------------------------------------------


class _Out:
    def __init__(self, as_json: bool) -> None:
        self.as_json = as_json
        self._last = 0.0

    def item(self, it: Item, *, throttle: bool = False) -> None:
        now = time.monotonic()
        if throttle and now - self._last < 0.5:
            return
        self._last = now
        if self.as_json:
            sys.stdout.write(json.dumps(it.event(), ensure_ascii=False) + "\n")
        else:
            size = (
                f" {it.done_bytes / 1e6:.0f}/{it.total_bytes / 1e6:.0f} MB"
                if it.total_bytes
                else ""
            )
            detail = it.message or it.path
            sys.stdout.write(f"{it.name:<11} {it.state:<12}{size}  {it.model}  {detail}\n")
        sys.stdout.flush()

    def done(self, ok: bool, code: int, its: list[Item]) -> None:
        if self.as_json:
            states = {i.name: i.state for i in its}
            sys.stdout.write(
                json.dumps({"event": "done", "ok": ok, "exit": code, "items": states}) + "\n"
            )
            sys.stdout.flush()


def _status(it: Item) -> None:
    """Fill ``it`` from what is on this machine (no network)."""
    if it.skip:
        it.state, it.message = "skipped", it.skip
        return
    if it.kind == "camel":
        it.state = "ready" if camel_installed() else "missing"
        return
    try:
        snap = hf_download(it, lambda _n, _t: None, local_only=True)
        if (snap / COMPLETE).is_file():
            it.path, it.state, it.source = str(snap), "ready", "cache"
            return
        it.message = "partly downloaded, or by a worker: run download to check it"
    except Exception:  # noqa: BLE001 - not in the cache
        pass
    if it.gated:
        from ..stt import model_share

        local = model_share.local_snapshots(it.model)
        if local:
            it.state, it.source, it.path = "ready", "share-copy", str(local[0].path)
            return
    it.state = "missing"


def _download(it: Item, out: _Out, share: Path | None) -> None:
    if it.skip:
        it.state, it.message = "skipped", it.skip
        out.item(it)
        return
    it.state = "downloading"
    out.item(it)
    if it.kind == "camel":
        try:
            camel_download()
            it.state, it.source = "ready", "camel"
        except Exception as exc:  # noqa: BLE001 - reported, the others go on
            it.state, it.message = "failed", f"{type(exc).__name__}: {exc}"[:300]
        out.item(it)
        return

    def on_bytes(n: int, total: int) -> None:
        it.done_bytes, it.total_bytes = n, total
        out.item(it, throttle=True)

    try:
        snap = hf_download(it, on_bytes, local_only=False)
        (snap / COMPLETE).write_text("ok\n", encoding="utf-8")
        it.path, it.state, it.source = str(snap), "ready", "hf"
    except Exception as exc:  # noqa: BLE001 - gated, offline, disk: reported
        login = it.gated and needs_login(exc)
        hf_error = f"{type(exc).__name__}: {exc}"[:300]
        if it.gated:
            from ..stt import model_share

            try:
                it.path = str(model_share.from_share(it.model, share))
                it.state, it.source = "ready", "share"
                it.message = "" if share else "a copy already here"
            except (OSError, ValueError, model_share.ShareModelError) as share_exc:
                it.state = "needs_login" if login else "failed"
                it.message = f"Hugging Face: {hf_error} | share: {share_exc}"[:400]
        else:
            it.state, it.message = "failed", hf_error
    out.item(it)


def _exit_code(its: list[Item]) -> int:
    states = {i.state for i in its}
    if states <= {"ready", "skipped"}:
        return EXIT_OK
    if states <= {"ready", "skipped", "needs_login"}:
        return EXIT_NEEDS_LOGIN
    return EXIT_FAILED


def add_parser(subparsers: Any) -> None:
    p = subparsers.add_parser(
        "models", help="The model files the workers load: what is here, and fetch the rest."
    )
    p.add_argument("what", choices=("status", "download"))
    p.add_argument(
        "--only",
        default="",
        help=f"comma-separated items (default: all of {', '.join(NAMES)})",
    )
    p.add_argument(
        "--share",
        default=os.environ.get("MAKTABA_MODELS_DIR") or None,
        help="the share's models folder to copy the gated model from when Hugging Face "
        "refuses (default: $MAKTABA_MODELS_DIR)",
    )
    p.add_argument("--json", action="store_true", help="one JSON object per line")


def _logs_to_stderr() -> None:
    """stdout carries the progress lines a caller parses: the log goes to stderr (the
    stream looked up per line, as the worker's logger does stdout)."""
    import structlog

    structlog.configure(logger_factory=lambda *_a: structlog.PrintLogger(file=sys.stderr))


def main(args: argparse.Namespace) -> int:
    _logs_to_stderr()
    only = {n.strip() for n in str(args.only).split(",") if n.strip()}
    unknown = only - set(NAMES)
    if unknown:
        sys.stderr.write(f"unknown item(s): {', '.join(sorted(unknown))} (of {', '.join(NAMES)})\n")
        return 2
    its = items(only or None)
    out = _Out(bool(args.json))
    if args.what == "status":
        for it in its:
            _status(it)
            out.item(it)
        code = EXIT_OK if {i.state for i in its} <= {"ready", "skipped"} else EXIT_MISSING
        out.done(code == EXIT_OK, code, its)
        return code
    share = Path(args.share).expanduser() if args.share else None
    for it in its:
        _download(it, out, share)
    code = _exit_code(its)
    out.done(code == EXIT_OK, code, its)
    return code
