"""Which language packs this machine can use (Epic 52, story 52.3).

A fixed table: each pack's code with the Epic 51 component of its data (``ar`` →
``lang.ar``, ``en`` → ``lang.en``, every language of ``hunspell-languages.toml`` →
``lang.hunspell.<code>``; the fallback needs only ``runtime.lang``). A pack is usable when
its component is wanted on this machine (an asset's want is ``installed`` or ``absent``)
and its folder is found (:func:`maktaba_pipeline.components.packs.pack_dir`); otherwise its
script falls to the fallback. The switch is the component's: this module only reads it,
and writes the ``language_packs`` report.

:func:`for_script` picks a run's pack: the transcript's language's pack when it serves the
run's script; a run in the transcript's own script without that pack goes to the
fallback (a Persian transcript never gets the Arabic dictionary); a run in another script
(code-switching: English words in an Arabic lecture) goes to that script's default pack.

:func:`Registry.versions` is part of ``word_check``'s fingerprint: each pack's code
version and the sha256 of its data files, so an update that lands the same bytes in a new
``<version>_<sha12>`` folder re-checks nothing.
"""

from __future__ import annotations

import hashlib
import tomllib
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from functools import lru_cache
from pathlib import Path
from typing import Any

from ..components import packs as component_packs
from ..components.store import Desired
from ..engine.sql import now, to_ts
from ..log import get_logger
from .ar import ArabicPack
from .base import LanguagePack
from .en import EnglishPack
from .fallback import FallbackPack
from .hunspell import HunspellPack

__all__ = [
    "DEFAULT_FOR_SCRIPT",
    "HUNSPELL_LANGUAGES",
    "PackRow",
    "Registry",
    "base_language",
    "data_sha256",
    "load",
    "script_of_language",
]

_log = get_logger()
_HERE = Path(__file__).resolve().parent

RUNTIME = "runtime.lang"


@dataclass(frozen=True, slots=True)
class HunspellLanguage:
    code: str
    script: str
    en: str
    ar: str
    licence: str


@lru_cache(maxsize=1)
def _hunspell_languages() -> tuple[HunspellLanguage, ...]:
    raw = tomllib.loads((_HERE / "hunspell-languages.toml").read_text(encoding="utf-8"))
    return tuple(
        HunspellLanguage(
            str(x["code"]), str(x["script"]), str(x["en"]), str(x["ar"]), str(x["licence"])
        )
        for x in raw.get("language", [])
    )


HUNSPELL_LANGUAGES = _hunspell_languages()

#: the dedicated packs: (code, component, scripts)
_DEDICATED: tuple[tuple[str, str, frozenset[str]], ...] = (
    ("ar", "lang.ar", frozenset({"Arab"})),
    ("en", "lang.en", frozenset({"Latn"})),
)

#: a run in a script other than the transcript's: the pack that script's words most often
#: are in this library (English in Arabic lectures, Arabic quoted in English talks)
DEFAULT_FOR_SCRIPT: dict[str, str] = {"Arab": "ar", "Latn": "en"}

#: languages Whisper hears whose script is not their pack's (for the fallback rule)
_SCRIPT_OF = {
    "ar": "Arab", "fa": "Arab", "ur": "Arab", "ps": "Arab", "sd": "Arab", "ug": "Arab",
    "he": "Hebr", "yi": "Hebr", "ru": "Cyrl", "uk": "Cyrl", "bg": "Cyrl", "sr": "Cyrl",
    "kk": "Cyrl", "mk": "Cyrl", "be": "Cyrl", "el": "Grek", "hi": "Deva", "mr": "Deva",
    "ne": "Deva", "bn": "Beng", "th": "Thai", "ka": "Geor", "hy": "Armn", "ko": "Hang",
    "ja": "Hani", "zh": "Hani", "yue": "Hani",
}  # fmt: skip

_RTL = frozenset({"ar", "fa", "ur", "ps", "sd", "ug", "he", "yi", "ckb"})


def base_language(code: str | None) -> str:
    return (code or "").split("-")[0].split("_")[0].lower()


def script_of_language(code: str | None) -> str:
    """The script a language is written in (Latin unless known otherwise)."""
    b = base_language(code)
    for h in HUNSPELL_LANGUAGES:
        if h.code == b:
            return h.script
    return _SCRIPT_OF.get(b, "Latn")


@lru_cache(maxsize=64)
def _sha(folder: str, stamp: tuple[tuple[str, int, float], ...]) -> str:
    h = hashlib.sha256()
    base = Path(folder)
    for rel, _size, _mtime in stamp:
        h.update(rel.encode() + b"\0")
        h.update((base / rel).read_bytes())
        h.update(b"\0")
    return h.hexdigest()


def data_sha256(folder: Path) -> str:
    """The sha256 over a pack folder's files (their paths and bytes, sorted): the same
    bytes in a new ``<version>_<sha12>`` folder give the same digest."""
    files = sorted(p for p in folder.rglob("*") if p.is_file() and not p.name.startswith("."))
    stamp = tuple(
        (p.relative_to(folder).as_posix(), p.stat().st_size, p.stat().st_mtime) for p in files
    )
    return _sha(str(folder), stamp)


@dataclass(slots=True)
class PackRow:
    """One row of the ``language_packs`` report."""

    code: str
    component: str | None
    version: str = ""
    data_sha256: str | None = None
    tokenizer: str | None = None
    state: str = "missing"  # loaded | off | missing | error
    error: str | None = None


@dataclass(slots=True)
class Registry:
    packs: dict[str, LanguagePack] = field(default_factory=dict)
    rows: list[PackRow] = field(default_factory=list)
    shas: dict[str, str] = field(default_factory=dict)
    #: runtime.lang's folder is on this machine (False: word_check waits for it)
    runtime: bool = True
    _fallbacks: dict[str, FallbackPack] = field(default_factory=dict)

    def fallback(self, language: str | None) -> FallbackPack:
        b = base_language(language) or "und"
        fb = self._fallbacks.get(b)
        if fb is None:
            fb = FallbackPack(b, "rtl" if b in _RTL else "ltr")
            self._fallbacks[b] = fb
        return fb

    def for_script(self, script: str, language: str | None) -> LanguagePack:
        """The pack for a run in ``script`` of a transcript in ``language``."""
        lang = base_language(language)
        own = self.packs.get(lang)
        if own is not None and script in own.scripts:
            return own
        if script == script_of_language(lang):
            return self.fallback(lang)
        default = DEFAULT_FOR_SCRIPT.get(script)
        if default is not None and default in self.packs:
            return self.packs[default]
        return self.fallback(lang if script == script_of_language(lang) else "und")

    def direction(self, language: str | None) -> str:
        pack = self.packs.get(base_language(language))
        if pack is not None:
            return pack.direction
        return "rtl" if base_language(language) in _RTL else "ltr"

    def version_of(self, pack: Any) -> str:
        code = str(getattr(pack, "code", ""))
        sha = self.shas.get(code) if code in self.packs and self.packs[code] is pack else None
        return f"{getattr(pack, 'pack_id', code)}:{code}:{pack.version}:{(sha or '')[:16]}"

    def versions(self) -> dict[str, str]:
        """Each usable pack's code version plus its data's digest."""
        return {code: self.version_of(p) for code, p in sorted(self.packs.items())}


Builder = Callable[[Path], LanguagePack]


def _table(analyse: Any = None) -> list[tuple[str, str, Builder]]:
    out: list[tuple[str, str, Builder]] = [
        ("ar", "lang.ar", lambda d: ArabicPack(d, analyse=analyse)),
        ("en", "lang.en", lambda d: EnglishPack(d)),
    ]
    for h in HUNSPELL_LANGUAGES:
        out.append(
            (
                h.code,
                f"lang.hunspell.{h.code}",
                lambda d, h=h: HunspellPack(h.code, d, frozenset({h.script})),  # type: ignore[misc]
            )
        )
    return out


def build(
    desired: Mapping[str, Desired] | None,
    base: Path | None = None,
    analyse: Any = None,
) -> Registry:
    """The packs usable on this machine: wanted (no row: as its folder says) and found."""
    reg = Registry()
    want = {} if desired is None else {k: v.want for k, v in desired.items()}
    # without a components folder (a developer's checkout, an older supervisor) the venv
    # is all there is: the fallback runs on what it has
    no_packs_here = base is None and component_packs.root() is None
    reg.runtime = want.get(RUNTIME, "installed") != "absent" and (
        no_packs_here or component_packs.pack_dir(RUNTIME, base) is not None
    )
    for code, comp, make in _table(analyse):
        w = want.get(comp)
        if w == "absent":
            if comp in want:
                reg.rows.append(PackRow(code, comp, state="off"))
            continue
        folder = component_packs.pack_dir(comp, base)
        if folder is None:
            if comp in want or code in ("ar", "en"):
                reg.rows.append(PackRow(code, comp, state="missing"))
            continue
        try:
            pack = make(folder)
            sha = data_sha256(folder)
        except Exception as exc:  # noqa: BLE001 - a broken pack falls to the fallback
            _log.warning("language_pack_failed", code=code, error=f"{type(exc).__name__}: {exc}")
            reg.rows.append(
                PackRow(code, comp, state="error", error=f"{type(exc).__name__}: {exc}"[:300])
            )
            continue
        reg.packs[code] = pack
        reg.shas[code] = sha
        reg.rows.append(
            PackRow(code, comp, pack.version, sha, getattr(pack, "tokenizer", None), "loaded")
        )
    fb = reg.fallback("und")
    reg.rows.append(PackRow("fallback", RUNTIME, fb.version, None, fb.tokenizer, "loaded"))
    return reg


_CACHE: dict[str, Any] = {}


async def load(db: Any, analyse: Any = None, base: Path | None = None) -> Registry:
    """This machine's registry, built again when desired state moved (``components_changed``
    moves ``component_meta.revision``, so installing ``lang.hunspell.fr`` needs no
    restart) or a pack's folder changed; its report written then."""
    from ..components import store
    from ..components.runner import current_machine

    try:
        rev = await store.revision(db)
    except Exception:  # noqa: BLE001 - no components tables (an old test database)
        rev = -1
    root = base or component_packs.root()
    stamp = _stamp(root)
    key = (rev, stamp, id(analyse), str(root))
    if _CACHE.get("key") == key:
        reg: Registry = _CACHE["reg"]
        return reg
    desired: Mapping[str, Desired] | None = None
    if rev >= 0:
        try:
            desired, _ = await store.desired(db, current_machine())
        except Exception:  # noqa: BLE001 - as if nothing were wanted
            desired = None
    reg = build(desired, base, analyse)
    _CACHE.update(key=key, reg=reg)
    await report(db, reg)
    return reg


def _stamp(root: Path | None) -> tuple[tuple[str, str], ...]:
    """What ``current`` names for each pack under ``root`` (a folder swap reloads)."""
    if root is None or not root.is_dir():
        return ()
    out = []
    for d in sorted(root.iterdir()):
        if d.name.startswith(("lang.", RUNTIME)):
            try:
                out.append((d.name, (d / "current").read_text(encoding="utf-8").strip()))
            except OSError:
                out.append((d.name, ""))
    return tuple(out)


def forget() -> None:
    """Drop the cached registry (tests)."""
    _CACHE.clear()


async def report(db: Any, reg: Registry) -> None:
    """The ``language_packs`` rows for what this machine loaded (a report: best effort)."""
    try:
        async with db.transaction():
            for r in reg.rows:
                await db.execute(
                    "INSERT INTO language_packs (code, component, version, data_sha256, tokenizer,"
                    " state, error, loaded_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)"
                    " ON CONFLICT (code) DO UPDATE SET component = excluded.component,"
                    " version = excluded.version, data_sha256 = excluded.data_sha256,"
                    " tokenizer = excluded.tokenizer, state = excluded.state,"
                    " error = excluded.error, loaded_at = excluded.loaded_at",
                    r.code,
                    r.component,
                    r.version,
                    r.data_sha256,
                    r.tokenizer,
                    r.state,
                    r.error,
                    to_ts(db, now()),
                )
    except Exception as exc:  # noqa: BLE001 - the report never stops a check
        _log.warning("language_packs_report_failed", error=f"{type(exc).__name__}: {exc}")
