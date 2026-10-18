"""A language model checks what each series is about.

The keyword tagger (:mod:`.topic_tags`) reads words, not meaning: «أكاذيب عدنان إبراهيم
على النبي» is a refutation, not a prophets' story; «تأهيل المصلحات» is not hadith because
its episodes quote some. A local model (Ollama) reads the series' name, its library and a
sample of its episode titles and names the topics it is about, from the tagger's own
list. Its answer then corrects the tags (:func:`apply`):

* a keyword topic the model does not name keeps ``damp`` of its score (it drops below
  the planner's thresholds, so no channel takes the series by it);
* a topic the model names that the keywords missed is added at ``added_score``;
* the ``genre:`` is the model's first broad topic (a sub-topic is never a genre);
* ``occasion:ramadan`` / ``hajj`` (about the occasion) follow the model too.

Pure except :func:`ask` (HTTP to the model). Settings: :class:`TagCheckConfig`.
"""

from __future__ import annotations

import hashlib
import json
import urllib.request
from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

from .topic_tags import SUBTOPICS, TOPIC_NAMES_AR, TOPICS

#: bumps when the prompt or the way answers are applied changes (answers are re-asked)
TAG_CHECK_VERSION = 1

#: what each topic means, for the model (the Arabic name plus a hint where it is easy
#: to confuse)
_HINTS: dict[str, str] = {
    "tajweed": "تعليم التجويد وأحكام التلاوة وتحفيظ القرآن",
    "quran_recitation": "تلاوة القرآن نفسه (قارئ يتلو)، لا شرحه",
    "tafsir": "شرح آيات القرآن ومعانيها وتدبرها",
    "hadith": "شرح كتب الحديث والسنة ومصطلح الحديث",
    "seerah": "سيرة النبي ﷺ وحياته وغزواته وشمائله",
    "prophets_stories": "قصص الأنبياء السابقين (نوح، إبراهيم، موسى، يوسف…)",
    "sahaba": "سير الصحابة",
    "islamic_history": "التاريخ الإسلامي والدول والحضارة",
    "aqeedah": "العقيدة والتوحيد والإيمان",
    "shubuhat": "الرد على الشبهات والإلحاد والمخالفين ونقد الدعاة المنحرفين",
    "akhirah": "الموت والقبر والقيامة والجنة والنار",
    "fiqh": "الفقه والأحكام والفتاوى",
    "raqaiq": "الرقائق وتزكية النفس والمواعظ",
    "akhlaq": "الأخلاق والآداب والسلوك",
    "dua_dhikr": "الدعاء والأذكار",
    "ruqyah": "الرقية الشرعية",
    "family_marriage": "الأسرة والزواج وتربية الأبناء",
    "youth": "قضايا الشباب",
    "women": "قضايا المرأة",
    "dawah": "الدعوة إلى الله ومناهجها",
    "current_affairs": "الأخبار والسياسة والقضايا المعاصرة",
    "arabic_language": "اللغة العربية والنحو",
    "stories": "قصص وعبر (غير قصص الأنبياء)",
    "nasheed": "أناشيد",
    "poetry": "شعر",
    "fasting": "الصيام وأحكامه (فقه الصيام)",
    "qiyam": "التراويح وقيام الليل",
    "zakat": "الزكاة والصدقة",
    "salah": "فقه الصلاة وأحكامها",
    "family_fiqh": "فقه الأسرة: النكاح والطلاق والنفقة والمواريث",
    "muamalat": "فقه المعاملات: البيع والربا والتجارة",
    "quran_competition": "مسابقات القرآن",
    "palestine": "فلسطين والقدس والأقصى وغزة",
}
_OCCASIONS = ("ramadan", "hajj")


@dataclass(frozen=True, slots=True)
class TagCheckConfig:
    #: off: the keyword tags stand as they are
    enabled: bool = True
    #: an Ollama server and model (any model it serves; Arabic matters)
    url: str = "http://localhost:11434"
    model: str = "qwen2.5:7b"
    #: episode titles shown to the model per series (spread over the series)
    sample_titles: int = 15
    #: at most this many topics per series
    max_topics: int = 3
    #: a keyword topic the model does not name keeps this share of its score
    damp: float = 0.3
    #: a topic the model names that the keywords missed gets this score
    added_score: float = 0.8
    timeout_s: float = 180.0
    #: clips too (step ``check-clips``): the model reads each short video's title and
    #: keeps only the keyword topics it is about, so a clip that only mentions a topic
    #: («هل يجوز الدعاء بحق الأنبياء») is no filler on it
    clips: bool = True
    #: clip titles per question to the model (more is faster, fewer is more careful: a
    #: 7B model judges 4 at a time nearly as well as one)
    clip_batch: int = 4
    #: only videos up to this long (the ones that air as filler)
    clip_max_minutes: float = 20.0


def sample(titles: Sequence[str], n: int) -> list[str]:
    """``n`` titles spread over the series (its start, middle and end)."""
    if len(titles) <= n:
        return list(titles)
    step = len(titles) / n
    return [titles[int(i * step)] for i in range(n)]


def prompt(name: str, library: str, titles: Sequence[str], cfg: TagCheckConfig) -> str:
    topics = "\n".join(f"- {t}: {_HINTS.get(t, TOPIC_NAMES_AR.get(t, t))}"
                       for t in (*TOPICS, *SUBTOPICS))  # fmt: skip
    eps = "\n".join(f"- {t}" for t in sample(titles, cfg.sample_titles))
    return (
        "صنّف سلسلة فيديو إسلامية لقنوات تلفزيونية حسب موضوعها.\n"
        f"المكتبة (القناة أو الشيخ): {library}\n"
        f"اسم السلسلة: {name}\n"
        f"عناوين بعض الحلقات:\n{eps}\n\n"
        "اختر المواضيع التي تدور حولها السلسلة فعلاً، من هذه القائمة فقط (المفتاح بالإنجليزية):\n"
        f"{topics}\n\n"
        "قواعد:\n"
        f"- من 1 إلى {cfg.max_topics} مواضيع، الأهم أولاً.\n"
        "- ذِكر كلمة في العنوان لا يجعلها موضوعاً: «الشيخ رمضان» اسم شخص وليس شهر رمضان، "
        "والرد على شخص يتكلم عن النبي ﷺ هو shubuhat وليس seerah ولا prophets_stories.\n"
        "- شرح كتاب حديث يتناول أبواب الفقه هو hadith (وقد يضاف fiqh)، "
        "وليس salah لمجرد باب الصلاة.\n"
        "- ramadan = السلسلة عن رمضان نفسه (أحكامه، فضائله، لياليه)؛ بثّها في رمضان لا يكفي. "
        "hajj بالمثل.\n"
        'أجب بـ JSON فقط: {"topics": ["..."], "ramadan": false, "hajj": false}'
    )


def input_hash(name: str, library: str, titles: Sequence[str], cfg: TagCheckConfig) -> str:
    src = json.dumps([TAG_CHECK_VERSION, cfg.model, cfg.sample_titles, cfg.max_topics, name,
                      library, sample(titles, cfg.sample_titles)], ensure_ascii=False)  # fmt: skip
    return hashlib.sha1(src.encode()).hexdigest()


@dataclass(frozen=True, slots=True)
class Verdict:
    topics: tuple[str, ...]
    occasions: tuple[str, ...]


def parse(answer: str | Mapping[str, Any], cfg: TagCheckConfig) -> Verdict | None:
    """The model's JSON as a verdict, keeping only known topics; None when unusable."""
    try:
        data = json.loads(answer) if isinstance(answer, str) else dict(answer)
    except (ValueError, TypeError):
        return None
    if not isinstance(data, dict):
        return None
    known = set(TOPICS) | set(SUBTOPICS)
    raw = data.get("topics") or []
    topics = tuple(dict.fromkeys(
        str(t).strip().removeprefix("topic:") for t in (raw if isinstance(raw, list) else [raw])
        if str(t).strip().removeprefix("topic:") in known))[: cfg.max_topics]  # fmt: skip
    occ = tuple(o for o in _OCCASIONS if data.get(o) is True)
    if not topics:
        return None
    return Verdict(topics, occ)


def apply(
    tags: Mapping[str, tuple[float, str]], v: Verdict, cfg: TagCheckConfig
) -> dict[str, tuple[float, str]]:
    """The series' tags corrected by the model's verdict (see the module docstring)."""
    out: dict[str, tuple[float, str]] = {}
    named = set(v.topics)
    for tag, (score, ev) in tags.items():
        ns, _, val = tag.partition(":")
        if ns == "genre":
            continue  # set below
        if "model: not about it" in ev or "book «" in ev:
            out[tag] = (score, ev)  # damped already, or the book it explains says so
        elif (ns == "topic" and val not in named) or (
            ns == "occasion" and val in _OCCASIONS and val not in v.occasions
        ):
            out[tag] = (round(score * cfg.damp, 3), f"{ev}; model: not about it")
        else:
            out[tag] = (score, ev)
    for t in v.topics:
        key = f"topic:{t}"
        if key not in out:
            out[key] = (cfg.added_score, "model")
        elif out[key][0] < cfg.added_score:
            out[key] = (cfg.added_score, out[key][1] + "; model")
    for o in v.occasions:
        key = f"occasion:{o}"
        if key not in out or out[key][0] < cfg.added_score:
            out[key] = (cfg.added_score, (out.get(key, (0, ""))[1] + "; model").lstrip("; "))
    broad = [t for t in v.topics if t in TOPICS]
    old_genre = next((tg for tg in tags if tg.startswith("genre:")), None)
    if broad:
        out[f"genre:{broad[0]}"] = (out[f"topic:{broad[0]}"][0], "model: primary topic")
    elif old_genre:
        out[old_genre] = tags[old_genre]
    return out


# --------------------------------------------------------------------------- clips

#: bumps when the clip prompt or the way its answers are applied changes
CLIP_CHECK_VERSION = 2


def clip_prompt(items: Sequence[tuple[str, str, Sequence[str]]], cfg: TagCheckConfig) -> str:
    """One question about ``items`` (library, title, its keyword topics), numbered from
    1: which of its keyword topics is each clip really about."""
    nl = "\n"

    def name(t: str) -> str:
        return f"{t} ({_HINTS.get(t, TOPIC_NAMES_AR.get(t, t))})"

    lines = nl.join(
        f"{n}. [{lib}] {title}{nl}   المرشحة: {'، '.join(name(t) for t in cands)}"
        for n, (lib, title, cands) in enumerate(items, 1)
    )
    return nl.join((
        "هذه عناوين مقاطع قصيرة من مكتبة فيديو إسلامية، تُعرض فواصلَ بين البرامج في قنوات "
        "حسب الموضوع. لكل مقطع مواضيع مرشحة من كلمات عنوانه؛ احتفظ فقط بما يدور حوله "
        "المقطع فعلاً.",
        "", lines, "",
        "قواعد:",
        "- prophets_stories: احتفظ به إذا كان المقطع عن نبي سابق نفسه أو قومه أو أحداث "
        "حياته، ولو جاء بصيغة سؤال («تأويل يوسف لرؤيا الملك»، «كيف عُذّب قوم شعيب»، «مناظرة "
        "إبراهيم مع النمرود»، «لماذا لم يسجد إبليس لآدم»). احذفه إذا ذُكر الأنبياء ذكراً "
        "عابراً: حكم فقهي («حكم من يعمل عمل قوم لوط»)، مسألة عقدية عامة («عصمة الأنبياء»، "
        "«الدعاء بحق الأنبياء»)، اسم برنامج أو مدرسة («ورثة الأنبياء»).",
        "- كل موضوع آخر بالمثل: كلمة في العنوان لا تكفي، المقطع يجب أن يكون عنه.",
        "- يجوز الاحتفاظ بأكثر من موضوع، أو بلا شيء.",
        f"أجب عن المقاطع كلها ({len(items)} مقطعاً) بـ JSON فقط: "
        '{"clips": [{"n": 1, "keep": ["..."]}, {"n": 2, "keep": []}]} '
        f"والقائمة فيها {len(items)} عنصراً.",
    ))  # fmt: skip


def clip_input_hash(library: str, title: str, cands: Sequence[str],
                    cfg: TagCheckConfig) -> str:  # fmt: skip
    src = json.dumps([CLIP_CHECK_VERSION, cfg.model, library, title, sorted(cands)],
                     ensure_ascii=False)  # fmt: skip
    return hashlib.sha1(src.encode()).hexdigest()


def parse_clips(answer: str | Mapping[str, Any],
                cands: Sequence[Sequence[str]]) -> dict[int, tuple[str, ...]]:  # fmt: skip
    """The topics the model keeps per clip number (1..len(cands)), only from that clip's
    candidates. A clip the answer leaves out is missing (asked again next run)."""
    try:
        data = json.loads(answer) if isinstance(answer, str) else dict(answer)
    except (ValueError, TypeError):
        return {}
    if not isinstance(data, dict):
        return {}
    rows = data.get("clips")
    if not isinstance(rows, list):
        return {}
    out: dict[int, tuple[str, ...]] = {}
    for row in rows:
        if not isinstance(row, dict):
            continue
        try:
            i = int(str(row.get("n")).strip())
        except ValueError:
            continue
        if not 1 <= i <= len(cands):
            continue
        raw = row.get("keep") or []
        vals = raw if isinstance(raw, list) else [raw]
        mine = set(cands[i - 1])
        out[i] = tuple(dict.fromkeys(
            str(t).strip().removeprefix("topic:") for t in vals
            if str(t).strip().removeprefix("topic:") in mine))  # fmt: skip
    return out


def clip_candidates(tags: Mapping[str, float]) -> tuple[str, ...]:
    """A clip's keyword topics worth asking about (those a channel could take it by)."""
    return tuple(sorted(t[6:] for t, s in tags.items() if t.startswith("topic:") and s >= 0.5))


def apply_clip(tags: Mapping[str, float], keep: Sequence[str],
               cfg: TagCheckConfig) -> dict[str, float]:  # fmt: skip
    """A clip's tags corrected by the model: a candidate topic it does not keep keeps
    ``damp`` of its score (and is no longer its genre); formats, occasions and topics it
    was not asked about stay as they are."""
    asked = set(clip_candidates(tags))
    gone = asked - set(keep)
    out: dict[str, float] = {}
    for tag, score in tags.items():
        ns, _, val = tag.partition(":")
        if ns == "genre" and val in gone:
            continue
        out[tag] = round(score * cfg.damp, 3) if ns == "topic" and val in gone else score
    if not any(t.startswith("genre:") for t in out):
        broad = [t for t in keep if t in TOPICS]
        if broad:
            out[f"genre:{broad[0]}"] = out[f"topic:{broad[0]}"]
    return out


def ask(text: str, cfg: TagCheckConfig) -> str:
    """One answer from the model (JSON mode, temperature 0)."""
    body = json.dumps({
        "model": cfg.model, "stream": False, "format": "json",
        "options": {"temperature": 0, "num_ctx": 4096},
        "messages": [{"role": "user", "content": text}],
    }).encode()  # fmt: skip
    req = urllib.request.Request(cfg.url.rstrip("/") + "/api/chat", data=body,
                                 headers={"Content-Type": "application/json"})  # fmt: skip
    with urllib.request.urlopen(req, timeout=cfg.timeout_s) as r:  # noqa: S310 — local model
        return str(json.loads(r.read().decode("utf-8"))["message"]["content"])


def _register_help() -> None:
    from maktaba_pipeline.settings import Help, register

    g = "Checking tags with a language model"
    register(TagCheckConfig, {
        "enabled": Help(g, "Check tags with a model", "A local model (Ollama) reads each "
                        "series' name and episode titles and says what it is about; the "
                        "keyword tags it does not confirm are damped. Off: keyword tags only."),
        "url": Help(g, "Model server (Ollama)"),
        "model": Help(g, "Model", "Any model the server has; it must read Arabic well."),
        "sample_titles": Help(g, "Episode titles shown per series", minimum=1),
        "max_topics": Help(g, "Topics per series (most)", minimum=1),
        "damp": Help(g, "Unconfirmed topic keeps (share of its score)", "0: dropped; 1: kept "
                     "as it is.", minimum=0),
        "added_score": Help(g, "Score of a topic only the model saw", minimum=0),
        "timeout_s": Help(g, "Answer timeout (s)", minimum=1),
        "clips": Help(g, "Check clips too", "The model reads each short video's title (step "
                      "check-clips): a clip that only mentions a topic is no filler on it."),
        "clip_batch": Help(g, "Clip titles per question", minimum=1),
        "clip_max_minutes": Help(g, "Clips up to (minutes)", "Only videos up to this long: "
                                 "the ones that air as filler.", minimum=0),

    })  # fmt: skip


_register_help()
