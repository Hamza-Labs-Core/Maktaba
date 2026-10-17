"""Topic tagging for detected series and single videos (Arabic-first).

Themed live channels (a Quran channel, a Seerah channel, a kids block, a
Ramadan schedule …) need to know *what a series is about*, *what shape it
has* and *who it is for*. Islamic lecture/program channels do not label
that anywhere machine-readable, but their text does: series names
(``قصص الأنبياء``, ``السيرة النبوية``), playlist titles, the recurring parts
of episode titles (``قصة نوح عليه السلام``), descriptions, and the channel
itself (a reciter's channel, a nasheed channel). This module turns that
text into namespaced tags with a confidence score and the evidence used.

Namespaces (:data:`NAMESPACES`):

- ``topic:``   what it is about — several per series, the strongest one is
  also emitted as ``genre:<topic>`` (the single value Epic 27 smart-mix
  balances on, ``ContentItem.genre``);
- ``format:``  program / lecture / khutbah / qa_fatwa / recitation / clips /
  documentary / drama / interview / podcast / live — see
  :data:`FORMAT_CONTENT_TYPE` for the mapping onto Story 26.2's
  ``video_classification.content_type`` vocabulary;
- ``audience:`` general / kids / women / youth (``general`` when nothing
  narrower is evidenced);
- ``occasion:`` ramadan / hajj / eid / ashura / jumuah / isra_miraj /
  hijri_new_year — for time-slotting (a Ramadan program in Ramadan, Surat
  al-Kahf on Friday).

How evidence is weighed (why each source counts what it does):

- **Where a phrase appears.** The series name and playlist titles are
  editorial labels (strong); episode titles count by the *share* of
  episodes that carry the phrase, so one stray mention in a 30-episode
  season barely moves the score; descriptions are weakest: only their
  opening counts (what follows the first link is pasted blocks, often
  another video's blurb), and channels paste the same footer under
  *every* video (``#السيرة_النبوية_نبيل_العوضي``) — callers pass
  :func:`boilerplate_phrases` of the whole channel as ``noise``; the
  channel/presenter name is a prior (a reciter's channel makes everything
  lean ``quran_recitation``) and, as :func:`branding`, is stripped from
  titles so ``| عثمان الخميس`` does not read as the Companion.
- **Phrase weight.** Unambiguous phrases (``السيرة النبوية``, ``تفسير``)
  weigh 1.0; words that hint but recur everywhere (``النبي``, ``القلب``)
  weigh < 0.5 and together add at most 0.35 per text. Context rules keep
  homographs apart: ``سورة`` is recitation unless the same title says
  ``تفسير``/``تدبر``/``وقفات`` …; the word after ``سورة`` is a surah name,
  never a topic (``سورة النساء`` is not about women); a bare prophet's or
  Companion's name needs story context (``المتسابق محمد شعيب`` is a
  contestant); a single word after ``عبد``/``أبو`` or before ``بن`` is part
  of a person's name (``عبد الرحمن``, ``عثمان بن أرطغرل``).
- **Conflicts** between readings are settled per series
  (:func:`_resolve_conflicts`): explanation beats recitation, a nasheed
  stays a nasheed whatever it praises, clip series are not programs.
- **Score** = ``1 - exp(-Σ source_weight × strength)``, so independent
  sources reinforce each other and saturate below 1.

Matching is on :func:`~maktaba_pipeline.text.arabic.fold`-ed tokens with
Arabic proclitics (و ف ب ل ك، لل) tolerated, so ``والسيرة``/``بالقرآن``/
``للأطفال`` hit ``السيرة``/``القرآن``/``الأطفال``; English fallbacks are
plain lowercase words (``seerah``, ``nasheed``, ``kids``).

Pure and deterministic, no I/O. :data:`TAGGER_VERSION` bumps when the
lexicon or scoring changes (stored tags should then be recomputed).
"""

from __future__ import annotations

import math
import re
from collections import Counter
from collections.abc import Iterable, Sequence
from dataclasses import dataclass
from datetime import date, timedelta
from typing import NamedTuple

from ..text.arabic import fold

__all__ = [
    "AUDIENCES",
    "FORMATS",
    "FORMAT_CONTENT_TYPE",
    "NAMESPACES",
    "OCCASIONS",
    "TAGGER_VERSION",
    "SUBTOPICS",
    "TOPICS",
    "TOPIC_NAMES_AR",
    "Tag",
    "TaggerConfig",
    "boilerplate_phrases",
    "branding",
    "content_type",
    "match_text",
    "primary_topic",
    "tag_series",
    "tag_video",
]

TAGGER_VERSION: int = 3

NAMESPACES: tuple[str, ...] = ("topic", "format", "audience", "occasion", "genre")

#: Ordered: ties between equally scored topics go to the earlier (more specific) one.
TOPICS: tuple[str, ...] = (
    "tajweed", "quran_recitation", "tafsir", "hadith", "seerah", "prophets_stories",
    "sahaba", "islamic_history", "aqeedah", "shubuhat", "akhirah", "fiqh", "raqaiq",
    "akhlaq", "dua_dhikr", "ruqyah", "family_marriage", "youth", "women", "dawah",
    "current_affairs", "arabic_language", "stories", "nasheed", "poetry",
)  # fmt: skip
#: Finer topics inside a broad one (fiqh → salah, zakat …; Ramadan → fasting, qiyam):
#: emitted as ``topic:`` tags for themed channels, never as the ``genre:`` (the broad
#: topic stays the genre, so collections and the mix balance keep their topics).
SUBTOPICS: tuple[str, ...] = (
    "fasting", "qiyam", "zakat", "salah", "family_fiqh", "muamalat", "quran_competition",
    "palestine",
)  # fmt: skip
#: Arabic names of the topics, for collection and channel titles ("<lib> - مقاطع - الفقه")
TOPIC_NAMES_AR: dict[str, str] = {
    "tajweed": "التجويد", "quran_recitation": "تلاوات", "tafsir": "التفسير",
    "hadith": "الحديث", "seerah": "السيرة النبوية", "prophets_stories": "قصص الأنبياء",
    "sahaba": "الصحابة", "islamic_history": "التاريخ الإسلامي", "aqeedah": "العقيدة",
    "shubuhat": "الشبهات", "akhirah": "الآخرة", "fiqh": "الفقه", "raqaiq": "الرقائق",
    "akhlaq": "الأخلاق", "dua_dhikr": "الدعاء والذكر", "ruqyah": "الرقية",
    "family_marriage": "الأسرة والزواج", "youth": "الشباب", "women": "المرأة",
    "dawah": "الدعوة", "current_affairs": "قضايا معاصرة", "arabic_language": "اللغة العربية",
    "stories": "قصص", "nasheed": "أناشيد", "poetry": "شعر", "general": "منوعات",
    "fasting": "الصيام", "qiyam": "التراويح والقيام", "zakat": "الزكاة والصدقة",
    "salah": "فقه الصلاة", "family_fiqh": "فقه الأسرة", "muamalat": "فقه المعاملات",
    "quran_competition": "مسابقات القرآن", "palestine": "فلسطين",
}  # fmt: skip
FORMATS: tuple[str, ...] = (
    "program", "lecture", "khutbah", "qa_fatwa", "recitation", "clips",
    "documentary", "drama", "interview", "podcast", "live",
)  # fmt: skip
#: ``kids_friendly``: not made for children but fine for them (a seerah series, a
#: reciter's or a history channel); ``beginners``: a basics programme
AUDIENCES: tuple[str, ...] = ("general", "kids", "kids_friendly", "beginners", "women", "youth")
OCCASIONS: tuple[str, ...] = (
    "ramadan", "hajj", "eid", "ashura", "jumuah", "isra_miraj", "hijri_new_year",
)  # fmt: skip

#: ``format:`` → Story 26.2 ``video_classification.content_type``
#: (``lecture | sermon | interview | documentary | film | music_video |
#: podcast | news | unknown``). ``recitation`` and ``clips`` have no
#: counterpart there; see :func:`content_type`.
FORMAT_CONTENT_TYPE: dict[str, str] = {
    "program": "lecture", "lecture": "lecture", "qa_fatwa": "lecture", "khutbah": "sermon",
    "interview": "interview", "documentary": "documentary", "drama": "film",
    "podcast": "podcast", "live": "lecture", "recitation": "unknown", "clips": "unknown",
}  # fmt: skip


class Tag(NamedTuple):
    tag: str  # "namespace:name"
    score: float  # 0..1
    evidence: str  # human-readable: which sources and phrases fired


@dataclass(frozen=True, slots=True)
class TaggerConfig:
    #: per-source weights (see module docstring for the reasoning)
    w_name: float = 3.0
    w_playlist: float = 2.0
    w_episodes: float = 2.5  # × share-weighted strength over episode titles
    w_description: float = 1.0  # × share over description heads (see _desc_head)
    w_channel: float = 0.6  # a prior: alone it yields ≤ 0.45, below most title evidence
    w_title: float = 3.0  # single video title (tag_video)
    #: tags below this score are dropped
    min_score: float = 0.3
    #: shorter than this median → clip-like
    clip_max_s: float = 300.0
    #: long-form default (lecture) when nothing names a format
    long_form_s: float = 1200.0
    #: share of exact air dates inside the occasion window to call a season seasonal
    occasion_share: float = 0.6
    min_dated_episodes: int = 5
    #: a season that aired ≥ this many episodes within this many days is a broadcast program
    program_min_episodes: int = 10
    program_window_days: int = 45


# ---------------------------------------------------------------------------
# Lexicon. Each tag maps weight → "|"-separated phrases (folded at import).
# Modifiers name a :data:`_CONTEXT` word set: ``phrase ~ctx`` is vetoed when a
# context word appears in the same text, ``phrase ^ctx`` needs one to appear,
# ``phrase <ctx`` is vetoed right after a context word; ``stem*`` matches any
# token starting with ``stem``.
# ---------------------------------------------------------------------------

_CONTEXT: dict[str, str] = {
    # a surah named in a title is a recitation unless the title explains it
    "explain": "تفسير التفسير تدبر التدبر تدبرات وقفات وقفة وقفه تأملات تاملات شرح معاني معنى "
    "قصة قصص خواطر خاطرة أسباب فضل فضائل إعجاز اعجاز درس دروس محاضرة لطائف هدايات "
    "تعليق التعليق يشرح يفسر الطبري القرطبي السعدي كثير معالجة "
    # a sermon or a study about a surah (نظرات في سورة الأنفال) is not its recitation
    "نظرات نظرة مدارسة دراسة إعراب اعراب مقاصد خطبة خطبه خطب الخطبة",
    # a prophet's bare name (يوسف، موسى، شعيب) is also a person's name: it counts
    # only next to story / prophet words
    "prophet": "قصة قصص عليه السلام نبي النبي الأنبياء نبيه قوم سيدنا رسول بني إسرائيل "
    "الحوت الطوفان الفلك فرعون",
    # likewise a Companion's bare name
    "sahaba": "رضي عنه عنها عنهما الصحابي الصحابية الصحابة قصة سيرة استشهاد مقتل أم المؤمنين",
    # "حفظ القرآن من التحريف" is about the text's preservation (apologetics)
    "tahrif": "التحريف تحريف المستشرق المستشرقين الأناجيل",
    # "العصر الحديث" is "modern times", not hadith (previous-word context)
    "modern": "العصر عصر التاريخ",
    # "إسرائيل" in "بني إسرائيل" is the prophets' story, not current affairs
    "bani": "بني بنو",
    # "السلام" after "عليه" is the blessing on a prophet, not the divine name
    "salam": "عليه عليها عليهم عليكم",
    # "الخلق" in "بداية الخلق" is creation, not character
    "creation": "بداية بدايه خلق خلقه",
    # "الصديق" after "يوسف" is the prophet's epithet
    "yusuf": "يوسف",
    # "أسئلة القبر" / "الأسئلة الأخيرة" are eschatology talks, not a Q&A session
    "eschat": "القبر الأخيرة الآخرة الملكين القيامة",
    # "شهر الرحمة" is Ramadan, not the virtue of mercy
    "month": "شهر",
    # "تحفة الأطفال" is a tajweed poem, not a children's programme
    "tuhfa": "تحفة التحفة",
    # "الصلاة على النبي" is a dhikr, not the prayer
    "nabi": "النبي نبي محمد الحبيب رسول الرسول المصطفى",
}

_LEXICON: dict[str, dict[float, str]] = {
    "topic:quran_recitation": {
        1.0: (
            "تلاوة | تلاوات | ترتيل | مرتل | المصحف المرتل | مجود | المصحف المجود | تلاوة خاشعة "
            "| ختمة | ختمه | تسجيلات الحرم | recitation | murattal | mujawwad | tilawat | tilawa "
            "| surah | surat | quranic | holy quran"
        ),
        0.8: (
            "سورة ~explain | سوره ~explain | سور ~explain | للقارئ | رواية حفص | رواية ورش "
            "| برواية | quran ~explain | koran | جزء عم ~explain"
        ),
        # a reciter can be a guest on a talk show: weak alone
        0.4: "القارئ | القارىء | قارئ",
        0.25: "القرآن ~explain | قرآن ~explain",
    },
    "topic:tajweed": {
        1.0: (
            "تجويد | التجويد | أحكام التجويد | مخارج الحروف | القراءات | القراءات العشر "
            "| الشاطبية | الجزرية | تحفة الأطفال | تحفيظ | الإقراء | tajweed | تصحيح التلاوة "
            "| تلقين | القلقلة | التجويدية | الأحكام التجويدية | النون الساكنة | الميم الساكنة "
            "| المدود | الإخفاء | الإظهار | الغنة | التحفة | السوسي | قالون | حفظ القرآن ~tahrif"
        ),
        0.5: "الوقف والابتداء | الإدغام | التلاوة الصحيحة",
    },
    "topic:tafsir": {
        1.0: (
            "تفسير | التفسير | تدبر | التدبر | تدبرات | أسباب النزول | علوم القرآن | قصة وآية "
            "| قصص القرآن | القصص القرآني | إعجاز القرآن | خواطر قرآنية | tafsir | tafseer "
            "| معاني سورة | تفسير القرآن | تفسير آيات | quran explanation"
        ),
        0.6: (
            "تأملات | وقفات مع سورة | وقفات | آيات | الآيات | الآية | لطائف قرآنية | هدايات "
            "| الإعجاز"
        ),
        0.5: "معاني | يفسر",
        0.3: "القرآن | قرآن | سورة | سوره | القرآني | القرانية | آية",
    },
    "topic:hadith": {
        1.0: (
            "أحاديث | الأحاديث | صحيح البخاري | البخاري | صحيح مسلم | الأربعين النووية "
            "| الأربعون النووية | رياض الصالحين | بلوغ المرام | عمدة الأحكام | مصطلح الحديث "
            "| الجرح والتعديل | سنن أبي داود | سنن الترمذي | سنن النسائي | ابن ماجه | موطأ "
            "| مسند أحمد | شرح السنة | hadith | ahadith | نخبة الفكر | علل الترمذي | العلل "
            "| الموطأ | الأربعون البخارية | مختصر البخاري | حديث <modern | الحديث <modern"
        ),
        0.5: (
            "السنة النبوية | السنن | قال رسول الله | عن النبي | الصحيحين | الترمذي "
            "| الأحاديث الضعيفة | ضعيف | التمييز"
        ),
        0.3: "السنة",
    },
    "topic:seerah": {
        1.0: (
            "السيرة النبوية | السيرة | سيرة | سيرة النبي | سيرة الرسول | الشمائل "
            "| الشمائل المحمدية | أخلاق الرسول | أخلاق النبي | حياة النبي | حياة الرسول "
            "| المولد النبوي | seerah | sirah | sira | prophet muhammad"
        ),
        0.6: (
            "غزوة | غزوات | الغزوات | الهجرة النبوية | البعثة | قريش | أبو طالب | عبد المطلب "
            "| الجاهلية | عام الفيل | بيعة العقبة | البيعتان | يتيم مكة | خديجة | أمهات المؤمنين "
            "| زوجات النبي | أزواج النبي | آل البيت | صلح الحديبية | فتح مكة | حجة الوداع "
            "| المشركين | أبي جهل | أبو جهل | أبو لهب | الطائف | يثرب | الحبيب | أبرهة | الأبابيل "
            "| أصحاب الفيل | ولد الهدى | غزوة بدر | غزوة أحد | الخندق"
        ),
        0.3: "مكة | المدينة | الكعبة | الوحي | الهجرة | جبريل",
        0.25: "النبي | الرسول | رسول الله",
        0.15: "صلى الله عليه وسلم",
    },
    "topic:prophets_stories": {
        1.0: (
            "قصص الأنبياء | قصة نبي | الأنبياء | الانبياء والرسل | أولو العزم | prophets "
            "| stories of the prophets"
        ),
        # a prophet's name alone is also a person's name: needs story context
        0.8: (
            "بني إسرائيل | بنو إسرائيل | كليم الله | ذا النون | ذو الكفل | قوم عاد | قوم ثمود "
            "| ثمود | سفينة نوح | يوسف الصديق | آدم ^prophet | نوح ^prophet | إبراهيم ^prophet "
            "| الخليل ^prophet | موسى ^prophet | هارون ^prophet | يونس ^prophet | أيوب ^prophet "
            "| شعيب ^prophet | لوط ^prophet | زكريا ^prophet | سليمان ^prophet | داود ^prophet "
            "| يعقوب ^prophet | إدريس ^prophet | إسماعيل ^prophet | إسحاق ^prophet "
            "| فرعون ^prophet | الطوفان ^prophet"
        ),
        0.5: (
            "عليه السلام | عليهم السلام | صالح عليه | نبي الله | هابيل | قابيل | إبليس "
            "| بداية الخلق | يوسف ^prophet | عيسى ^prophet | المسيح ^prophet | هود ^prophet "
            "| مريم ^prophet"
        ),
    },
    "topic:sahaba": {
        1.0: (
            "الصحابة | صحابة | الصحابي | صحابي | الصحابيات | أصحاب محمد | أصحاب النبي "
            "| أصحاب رسول الله | الخلفاء الراشدين | الخلفاء الراشدون | العشرة المبشرين | sahaba "
            "| companions | خير القرون | الصحابة الكرام"
        ),
        0.6: (
            "رضي الله عنه | رضي الله عنها | رضي الله عنهم | أبو بكر | أبي بكر | الصديق ~yusuf "
            "| الفاروق | عمر بن الخطاب | عثمان بن عفان | ذو النورين | علي بن أبي طالب "
            "| خالد بن الوليد | سعد بن أبي وقاص | أبو عبيدة | بلال بن رباح | مصعب بن عمير "
            "| فاطمة الزهراء | سلمان الفارسي | أبو هريرة | ابن عباس | ابن مسعود | حمزة ^sahaba "
            "| عائشة ^sahaba | معاوية ^sahaba | الزبير ^sahaba | طلحة ^sahaba | رضي الله عنهما "
            "| عمر الفاروق | سيف الله المسلول"
        ),
        # not "علي"/"عمر"/"عثمان" alone: "على" folds to "علي", عمر is also "age"
        0.4: "الأنصار | المهاجرين",
    },
    "topic:islamic_history": {
        1.0: (
            "التاريخ الإسلامي | تاريخ الإسلام | الدولة الأموية | الدولة العباسية "
            "| الدولة العثمانية | الخلافة العثمانية | الأندلس | الحروب الصليبية | صلاح الدين "
            "| الفتوحات الإسلامية | المغول | التتار | المماليك | السلاجقة | الفاطميين | كربلاء "
            "| islamic history"
        ),
        0.6: (
            "تاريخ | التاريخ | تاريخنا | الخلافة | الفتوحات | معركة | معارك | حضارة | الحضارة "
            "| الفتنة | الحسين | history | تاريخ* | العثمانيين | العثمانيون | أرطغرل "
            "| القسطنطينية | المرابطين | الموحدين | الأمويين | العباسيين | الصليبيين | ottoman "
            "| andalus | caliphate"
        ),
        0.3: "الدولة | سقوط",
    },
    "topic:aqeedah": {
        1.0: (
            "العقيدة | عقيدة | التوحيد | توحيد | أسماء الله الحسنى | الأسماء والصفات | أسماء الله "
            "| صفات الله | معرفة الله | يا الله | الشرك | نواقض الإسلام | كتاب التوحيد "
            "| العقيدة الطحاوية | الواسطية | القضاء والقدر | أركان الإيمان | aqeedah | aqidah "
            "| tawheed | tawhid | ذو الجلال | اسم الله | allah's names | names of allah "
            "| attributes of allah | creed"
        ),
        0.6: "attributes",
        0.5: (
            "الإيمان | الإيمان بالله | الشرك بالله | الكفر | البدعة | البدع | الغيب | الملائكة "
            "| الجن | السحر | الكهانة | الولاء والبراء | المنهج | السلف | أهل السنة | الخالق"
        ),
        0.4: (
            "الرحمن | الرحيم | القدوس | السلام ~salam | المهيمن | الجبار | المتكبر | البارئ "
            "| المصور | الغفار | الغفور | القهار | الوهاب | الرزاق | الفتاح | اللطيف | الخبير "
            "| الحليم | الودود | الحي القيوم | الأول والآخر"
        ),
        0.3: "سبحانه وتعالى",
    },
    "topic:shubuhat": {
        1.0: (
            "شبهات | الشبهات | شبهة | رد الشبهات | الرد على | الإلحاد | الالحاد | إلحاد "
            "| الملحدين | ملحد | الملاحدة | المستشرقين | الاستشراق | التنصير | النصرانية "
            "| المسيحية | المنصرين | العلمانية | النسوية | الليبرالية | مناظرة | مناظرات | الشيعة "
            "| الرافضة | القرآنيين | نظرية التطور | داروين | وجود الله | اللادينية | atheism "
            "| refutation | debate | apologetics | التحريف | الأناجيل | الإنجيل | التوراة "
            "| الكتاب المقدس | نصراني | النصارى | الطعن | الماسونية | الصوفية | شحرور "
            "| antishubohat | shubohat | shubuhat | refuting | atheist | atheists | christian "
            "| christians | bible | islamophobia | ألحد | ملحدة"
        ),
        0.5: (
            "رد | يرد | نقد | حوار مع ملحد | الأديان | مقارنة الأديان | الإعجاز العلمي "
            "| responding | response | يفضح | كشف أكاذيب"
        ),
    },
    "topic:akhirah": {
        1.0: (
            "علامات الساعة | أشراط الساعة | يوم القيامة | القيامة | اليوم الآخر | الآخرة "
            "| الدار الآخرة | البرزخ | عذاب القبر | نعيم القبر | يوم الميعاد | الحشر | الصراط "
            "| الدجال | المهدي | يأجوج ومأجوج | نهاية العالم | الجنة والنار | وصف الجنة "
            "| وصف النار | سكرات الموت | أحوال القبر | سد يأجوج ومأجوج"
        ),
        0.5: (
            "الجنة | النار | جهنم | القبر | الموت | الحساب | الميزان | الشفاعة | الحوض | البعث "
            "| الفردوس | حسن الخاتمة | سوء الخاتمة"
        ),
    },
    "topic:fiqh": {
        1.0: (
            "فقه | الفقه | أحكام | الأحكام | الطهارة | الوضوء | الغسل | التيمم | الزكاة | البيوع "
            "| المعاملات | الربا | المواريث | الميراث | الفرائض | أصول الفقه | القواعد الفقهية "
            "| زاد المستقنع | عمدة الفقه | الروض المربع | فقه الصيام | فقه الحج | فقه الصلاة "
            "| مناسك | المناسك | سجود السهو | مخالفات | أخطاء المصلين | fiqh | الاعتكاف | النجاسة "
            "| نجاسات | المذهب | المذاهب | التمذهب | فقه الاختلاف | أخصر المختصرات | الورقات "
            "| كتاب النكاح | كتاب الطهارة | الأضحية | زكاة"
        ),
        0.5: (
            "حكم | ما حكم | يجوز | هل يجوز | حلال | حرام | الحلال والحرام | الصلاة | صلاة "
            "| الصيام | صيام | الحج | العمرة | الطلاق | النكاح | الحجاب | سنن | مسائل | المسائل"
        ),
    },
    # finer fiqh / Ramadan / Quran topics (SUBTOPICS): tags only, never a genre ----------
    "topic:fasting": {
        1.0: (
            "الصيام | صيام | الصوم | صوم | فقه الصيام | أحكام الصيام | المفطرات | مفطرات "
            "| الصائم | الصائمين | للصائم | السحور | الإفطار | إفطار | زكاة الفطر | الاعتكاف "
            "| ليلة القدر | صيام التطوع | صيام الست | يوم عرفة | fasting | sawm"
        ),
        0.5: "رمضان | رمضانيات | الصائمون | الفطر",
    },
    "topic:qiyam": {
        1.0: (
            "التراويح | تراويح | صلاة التراويح | قيام الليل | قيام رمضان | التهجد | تهجد "
            "| صلاة الوتر | الشفع والوتر | دعاء القنوت | القنوت | taraweeh | tarawih | qiyam "
            "| tahajjud"
        ),
        0.5: "الوتر | القيام | ليالي رمضان",
    },
    "topic:zakat": {
        1.0: (
            "الزكاة | زكاة | زكاة المال | زكاة الفطر | زكاة الذهب | النصاب | نصاب | الصدقة "
            "| الصدقات | صدقة | صدقة جارية | الإنفاق | إنفاق | الإنفاق في سبيل الله | zakat "
            "| sadaqah | charity"
        ),
        0.5: "الفقراء والمساكين | المحتاجين | الوقف",
    },
    "topic:salah": {
        1.0: (
            "فقه الصلاة | أحكام الصلاة | صفة الصلاة | صفة صلاة النبي | الطهارة | الوضوء | الغسل "
            "| التيمم | سجود السهو | سجود التلاوة | الأذان | صلاة الجماعة | صلاة الجمعة "
            "| صلاة المسافر | قصر الصلاة | الجمع بين الصلاتين | أخطاء المصلين | المسح على الخفين "
            "| النجاسة | نجاسات | مواقيت الصلاة | شروط الصلاة | أركان الصلاة | واجبات الصلاة "
            "| مبطلات الصلاة | الخشوع في الصلاة | صلاة الاستخارة | صلاة الكسوف | صلاة الاستسقاء "
            "| السنن الرواتب | تارك الصلاة | كتاب الطهارة | كتاب الصلاة | salah | prayer | wudu"
        ),
        0.6: "الصلاة ~nabi | صلاة ~nabi | الصلوات | المصلي | المصلين | المسجد | المساجد",
    },
    "topic:family_fiqh": {
        1.0: (
            "النكاح | كتاب النكاح | عقد النكاح | عقد الزواج | الطلاق | طلاق | الخلع | العدة "
            "| الحضانة | النفقة | الرضاع | الرضاعة | المواريث | الميراث | الفرائض | علم الفرائض "
            "| تقسيم التركة | التركة | الوصية | الولي | المهر | الصداق | تعدد الزوجات | التعدد "
            "| الظهار | الإيلاء | اللعان | حقوق الزوج | حقوق الزوجة | حقوق الزوجين | فقه الأسرة "
            "| أحكام الأسرة | inheritance | divorce"
        ),
        0.5: "الزوجين | الزواج | الزوجة | الزوج",
    },
    "topic:muamalat": {
        1.0: (
            "المعاملات | المعاملات المالية | فقه المعاملات | البيوع | كتاب البيوع | فقه البيوع "
            "| الربا | الفوائد البنكية | البنوك | البنوك الإسلامية | بنك | التجارة | تجارة "
            "| التداول | تداول | الأسهم | البورصة | العملات الرقمية | البيتكوين | الفوركس "
            "| التقسيط | القرض | القروض | الديون | الإجارة | التأمين | الغرر "
            "| الاحتكار | الكسب الحلال | المال الحرام | الاقتصاد الإسلامي | الشركات | المضاربة "
            "| المرابحة | الصرف | riba | trading | finance | crypto | stocks"
        ),
        0.6: "بيع | البيع | المال | الأموال | الاقتصاد | الأرباح | الربح | السوق | الأسواق",
    },
    "topic:quran_competition": {
        1.0: (
            "مسابقة القرآن | مسابقة قرآنية | مسابقات القرآن | المسابقة القرآنية | مسابقة حفظ "
            "| مسابقة تحفيظ | جائزة دبي الدولية للقرآن | جائزة دبي | المسابقة الدولية "
            "| مسابقة دولية | جائزة القرآن | المتسابق | المتسابقين | المتسابقة | المتسابقون "
            "| تحدي القراء | quran competition"
        ),
        0.6: "مسابقة | مسابقات | المسابقة | جائزة | الجائزة | التصفيات | التحكيم | لجنة التحكيم",
    },
    "topic:palestine": {
        1.0: (
            "فلسطين | الفلسطينية | الفلسطيني | الفلسطينيين | غزة | قطاع غزة | القدس "
            "| المسجد الأقصى | الأقصى | طوفان الأقصى | الضفة الغربية | الكيان الصهيوني "
            "| الصهيونية | الصهاينة | المقاومة الفلسطينية | القسام | الانتفاضة | النكبة | نابلس "
            "| جنين | رفح | خان يونس | بيت المقدس | palestine | gaza | aqsa | jerusalem"
        ),
        # alone these name Israel, not Palestine (another country's ties with it)
        0.3: "الاحتلال | الصهيوني | إسرائيل <bani | المستوطنين | المستوطنون | الضفة",
    },
    "topic:raqaiq": {
        1.0: (
            "الرقائق | رقائق | التزكية | تزكية النفس | تزكية | أعمال القلوب | محاسبة النفس "
            "| حاسب نفسك | التوبة | توبة | الخشوع | الإخلاص | الزهد | الورع | مواعظ | موعظة "
            "| حلقة إيمانية | إيمانية | التربية العبادية | الثبات | tazkiyah | raqaiq"
        ),
        0.5: (
            "القلب | القلوب | الاستغفار | الصبر | الشكر | التوكل | الخوف | الرجاء | التقوى "
            "| الدنيا | النفس | العبادة | عبادة | الطاعة | المعاصي | الذنوب | الهداية | السعادة "
            "| الابتلاء | البلاء | الهم | الحزن | الأمل | الحياء | خواطر | خاطرة | تارك الصلاة "
            "| الغفلة | الجزع | الغفران | المغفرة | الأبرار | فضائل | فضائل الأعمال | خوف | قلوب"
        ),
        0.4: "فضل",
    },
    "topic:akhlaq": {
        1.0: (
            "الأخلاق | أخلاق | مكارم الأخلاق | مكارم | حسن الخلق | الخلق ~creation | الآداب "
            "| آداب | الأدب | بر الوالدين | صلة الرحم | حق الجار | الجار | الراحمون | ارحموا "
            "| akhlaq | manners"
        ),
        0.5: (
            "الرحمة ~month | الصدق | الكذب | الغيبة | النميمة | التسامح | سامح | التواضع "
            "| الأمانة | الإحسان | الكرم | العدل | الظلم | الحسد | الكبر | الغضب | الوفاء "
            "| الوالدين | والديك | العمل الخيري | التطوع | الإخاء | الأخوة | العقوق | صلة رحمك "
            "| جارك | إطعام الطعام"
        ),
    },
    "topic:dua_dhikr": {
        1.0: (
            "الدعاء | دعاء | أدعية | الأدعية | الأذكار | أذكار | أذكار الصباح | أذكار المساء "
            "| حصن المسلم | التسبيح | الباقيات الصالحات | فضل الذكر | مجالس الذكر | dua | dhikr "
            "| adhkar"
        ),
        0.5: (
            "الذكر | الاستغفار | الصلاة على النبي | لا حول ولا قوة إلا بالله | سبحان الله "
            "| الحمد لله"
        ),
    },
    "topic:ruqyah": {
        1.0: "الرقية | الرقية الشرعية | رقية | العين والحسد | علاج السحر | المس | ruqyah | ruqya",
    },
    "topic:family_marriage": {
        1.0: (
            "الزواج | زواج ~bani | الحياة الزوجية | الزوجين | الأسرة | الأسرة المسلمة "
            "| تربية الأبناء | تربية الأولاد | تربية الأطفال | العلاقة الزوجية | الخطوبة | الطلاق "
            "| marriage | parenting | الخلع | المسيار | الإصلاح بين الزوجين"
        ),
        0.5: "الزوج | الزوجة | الأبناء | الأولاد | الأم | الأب | البيت | البيوت | الأمومة | الحماة",
    },
    "topic:youth": {
        1.0: "الشباب | شباب | للشباب | المراهقين | المراهقة | الجامعة | الطلاب | youth",
        0.5: (
            "إدمان | مدمن | المخدرات | الشهوات | الإباحية | العادة السرية | السوشال ميديا | الصحبة"
        ),
    },
    "topic:women": {
        1.0: (
            "المرأة | النساء | نساء | نسائية | الحجاب | حجاب | النقاب | الصحابيات "
            "| المرأة المسلمة | حقوق المرأة | خير نساء العالمين | أمهات المؤمنين | للنساء "
            "| الأخوات | women | sisters | hijab"
        ),
        0.5: "الفتاة | الفتيات | المحجبة | العفة | الزينة | التبرج | الأم",
    },
    "topic:dawah": {
        1.0: (
            "الدعوة إلى الله | الدعوة | الدعوة الإسلامية | الدعاة | داعية | الأمر بالمعروف "
            "| النهي عن المنكر | قصة إسلام | أسلم | أسلمت | اعتنق الإسلام | المسلمين الجدد "
            "| مسلم جديد | dawah | revert | new muslim | convert | convert to islam"
        ),
        0.5: "دعوة | غير المسلمين | التعريف بالإسلام | نعمة الإسلام | البوذيين | الهندوس",
        0.3: "الإسلام",
    },
    "topic:current_affairs": {
        1.0: (
            "غزة | فلسطين | الأقصى | المسجد الأقصى | القدس | الاحتلال | الصهاينة | الصهيوني "
            "| السياسة | سياسي | الانتخابات | الثورة | الربيع العربي | الحرب على | الأخبار | نشرة "
            "| الإعلام | كورونا | الواقع | قضايا معاصرة | النوازل | news | politics | gaza "
            "| palestine | أخبار"
        ),
        0.5: (
            "المسلمون اليوم | الغرب | السوشال ميديا | التفاهة | صناعة التفاهة | الحمية | الاقتصاد "
            "| العالم اليوم | الرئيس | ترامب | نتنياهو | إيران | الدولار | الأنظمة | الحكام "
            "| الانقلاب | الانقلابات | المقاومة | الكيان | غوانتنامو | المستوطنين | المستوطنون "
            "| الضفة"
        ),
        0.15: "سوريا | العراق | اليمن | السودان | مصر | تونس",
    },
    "topic:arabic_language": {
        1.0: (
            "النحو | اللغة العربية | الآجرومية | ألفية ابن مالك | البلاغة | الصرف | الإعراب "
            "| قطر الندى | العروض | arabic grammar"
        ),
    },
    "topic:stories": {
        1.0: (
            "قصص وعبر | قصة وعبرة | أروع القصص | قصص مؤثرة | القصص المؤثرة | حكايات | حكاية "
            "| شخصيات وعبر | stories"
        ),
        # a story *about* a prophet or a verse is that topic first; storytelling second
        0.6: "قصص | القصص | قصة | عبر | العبر | story",
        0.4: "عجيبة | مؤثرة | موقف | مواقف",
    },
    "topic:nasheed": {
        1.0: (
            "نشيد | أناشيد | الأناشيد | أنشودة | انشودة | إنشاد | منشد | المنشد | بدون موسيقى "
            "| بدون إيقاع | nasheed | nasheeds | anasheed | نشيدة | انشوده | نشيده"
        ),
        # not «بصوت» ("in the voice of"): it credits any voice, a reciter's
        # (بصوت القارئ) or an audiobook narrator's as much as a munshid's
        0.6: "ابتهالات | ابتهال | مدائح | مديح | إيقاع",
    },
    # formats -------------------------------------------------------------
    "format:program": {
        1.0: "برنامج | برامج | البرنامج | program | show",
        0.5: "الحلقة | حلقة | حلقات | الحلقات | الموسم | episode | season",
    },
    "format:lecture": {
        1.0: (
            "محاضرة | محاضرات | المحاضرة | درس | دروس | الدرس | شرح | مجلس | المجلس | دورة "
            "| دورة علمية | lecture | lectures | lesson | class | dars | الدورة"
        ),
        0.5: "كلمة | كلمات | ندوة | الندوة | خاطرة",
    },
    "format:khutbah": {
        1.0: (
            "خطبة | خطب | الخطبة | خطبة الجمعة | خطبة العيد | خطبة عرفة | khutbah | khutba | sermon"
        ),
    },
    "format:qa_fatwa": {
        1.0: (
            "فتاوى | فتاوي | الفتاوى | فتوى | الفتوى | سؤال وجواب | أسئلة وأجوبة | ما حكم "
            "| هل يجوز | استفتاء | الأسئلة والأجوبة | fatwa | q&a | qa | فتاوى الصيام"
        ),
        0.5: "سؤال ~eschat | يسأل | أسئلة ~eschat | الأسئلة ~eschat | سائل",
    },
    "format:recitation": {
        1.0: (
            "تلاوة | تلاوات | ترتيل | مرتل | مجود | المصحف المرتل | المصحف المجود | ختمة "
            "| recitation | murattal | mujawwad | surah | surat"
        ),
        0.6: "سورة ~explain | سوره ~explain | للقارئ",
    },
    "format:clips": {
        1.0: (
            "مقاطع | مقطع | مقتطفات | مختارات | مقاطع مميزة | مقطع مؤثر | لقطات | shorts | clips "
            "| clip | highlights | short"
        ),
        0.5: "دقائق | دقيقة",
    },
    "format:documentary": {
        1.0: "وثائقي | فيلم وثائقي | الوثائقي | تقرير | جولة | رحلة إلى | documentary",
        0.5: "فيلم | أفلام | متحف | مجمع الملك فهد",
    },
    "format:drama": {
        1.0: (
            "مسلسل | المسلسل | دراما | تمثيلية | مسرحية | رسوم متحركة | كرتون | أنيميشن | drama "
            "| cartoon | animation"
        ),
    },
    "format:interview": {
        1.0: (
            "لقاء | لقاء مع | مقابلة | حوار مع | ضيف | ضيوف | استضافة | في ضيافة | في ضيافتهم "
            "| بكل صراحة | ساعة صراحة | interview"
        ),
        0.5: "حوار",
    },
    "format:podcast": {
        1.0: "بودكاست | البودكاست | بودكاستات | podcast",
    },
    "format:live": {
        1.0: "بث مباشر | البث المباشر | بثوث | بث | مباشر | live | stream | livestream",
    },
    # audiences -----------------------------------------------------------
    "audience:kids": {
        1.0: (
            "أطفال ~tuhfa | الأطفال ~tuhfa | للأطفال | الطفل | للطفل | الصغار | للصغار | براعم "
            "| أشبال | كرتون "
            "| رسوم متحركة | أنيميشن | أنشودة أطفال | kids | children | child | for kids "
            "| nursery | أناشيد أطفال"
        ),
    },
    "audience:beginners": {
        1.0: (
            "أساسيات | الأساسيات | للمبتدئين | المبتدئين | مبادئ | المبادئ | مدخل إلى | مدخل "
            "| بداية الطريق | خطوة بخطوة | تعلم | تعليم | تبسيط | مبسط | المبسط | ميسر | الميسر "
            "| basics | beginners | introduction | intro to"
        ),
    },
    "audience:women": {
        1.0: "نسائية | للنساء | للأخوات | الأخوات | يا أختي | أختي | للمرأة | للفتيات | sisters",
        0.5: "النساء | المرأة",
    },
    "audience:youth": {
        1.0: "للشباب | يا شباب | الشباب | للمراهقين | youth | teens",
    },
    # occasions -----------------------------------------------------------
    "occasion:ramadan": {
        1.0: (
            "رمضان | رمضانية | شهر رمضان | التراويح | تراويح | العشر الأواخر | ليلة القدر "
            "| الصيام | شهر الصيام | شهر الرحمة والغفران | الإفطار | السحور | ramadan"
        ),
        0.6: "القيام",
        0.4: "الليالي المباركات | صيام",
    },
    "occasion:hajj": {
        1.0: (
            "الحج | حج | مناسك | يوم عرفة | عرفة | العشر من ذي الحجة | ذي الحجة | عشر ذي الحجة "
            "| الأضحية | الحجاج | التلبية | hajj"
        ),
        0.4: "منى",
    },
    "occasion:eid": {
        1.0: "العيد | عيد الفطر | عيد الأضحى | خطبة العيد | يوم العيد | الأعياد | eid",
    },
    "occasion:ashura": {
        1.0: "عاشوراء | يوم عاشوراء | محرم | شهر الله المحرم | ashura",
    },
    "occasion:jumuah": {
        1.0: "الجمعة | يوم الجمعة | خطبة الجمعة | سورة الكهف | jumuah | jummah | friday",
    },
    "occasion:isra_miraj": {
        1.0: "الإسراء والمعراج | الإسراء | المعراج",
    },
    "occasion:hijri_new_year": {
        1.0: "رأس السنة الهجرية | العام الهجري | السنة الهجرية | نهاية العام | العام الجديد",
    },
    "topic:poetry": {
        1.0: (
            "قصيدة | قصائد | شعر | الشعر | إلقاء | ديوان | المتنبي | امرؤ القيس | الشافعي شعرا "
            "| poetry | poem"
        ),
        0.5: "أبيات | بيت شعر | معلقة",
    },
}

#: channel/presenter priors the lexicon cannot know: reciters, by full name
#: (a bare surname such as "العجمي" or "الغامدي" is shared by many people).
_RECITERS = (
    "عبد الباسط | عبدالباسط | محمود خليل الحصري | الحصري | محمد صديق المنشاوي | المنشاوي "
    "| سعد الغامدي | سعود الشريم | عبد الرحمن السديس | مشاري العفاسي | العفاسي "
    "| ماهر المعيقلي | أحمد العجمي | ياسر الدوسري | محمد الطبلاوي | مصطفى إسماعيل "
    "| أبو بكر الشاطري | ناصر القطامي | مزامير | abdul basit | basit | hussary | husary "
    "| minshawi | ghamdi | shuraim | sudais | afasy | alafasy | muaiqly | qatami | shatri"
)

# a single-word hit right after these is part of a person's name (عبد الرحمن, أبو بكر)
_NAME_BEFORE = frozenset({"عبد", "عبدال", "ابو", "ابي", "ام"})
# … or right before these (عثمان بن أرطغرل, يوسف بن تاشفين, صالح بن فوزان)
_NAME_AFTER = frozenset({"بن", "ابن", "بنت"})
# the word after "سورة" is a surah's name (النساء، الحج، مريم، الجمعة), not a topic
_SURAH_WORDS = frozenset({"سوره", "سور", "لسوره", "بسوره", "وسوره"})

_PROCLITICS = ("وب", "ول", "وك", "فب", "فل", "و", "ف", "ب", "ل", "ك")
_LIL = ("ولل", "فلل", "لل", "وبال", "بال", "وال", "فال", "كال")

#: weights below this are hints; a text's hints together add at most _HINT_CAP
_HINT = 0.5
_HINT_CAP = 0.35


@dataclass(frozen=True, slots=True)
class _Entry:
    tag: str
    phrase: str  # folded, space-joined (also the key used for noise)
    words: tuple[str, ...]
    weight: float
    veto: frozenset[str]  # any of these in the text cancels the hit
    require: frozenset[str]  # one of these must be in the text (empty: no requirement)
    not_after: frozenset[str]  # the previous token must not be one of these
    wildcard: bool  # last word is a prefix


def _token_forms(tok: str) -> frozenset[str]:
    """The token and its readings with Arabic proclitics (و ف ب ل ك، لل، ال) removed."""
    out = {tok}
    for p in _LIL:
        if tok.startswith(p) and len(tok) - len(p) >= 2:
            out.add("ال" + tok[len(p) :])
    for p in _PROCLITICS:
        if tok.startswith(p) and len(tok) - len(p) >= 3:
            out.add(tok[len(p) :])
    for f in list(out):
        if f.startswith("ال") and len(f) >= 5:
            out.add(f[2:])
    return frozenset(out)


def _compile() -> tuple[dict[str, list[_Entry]], list[_Entry]]:
    index: dict[str, list[_Entry]] = {}
    wild: list[_Entry] = []
    ctx = {k: frozenset(fold(v).split()) for k, v in _CONTEXT.items()}
    groups = [(t, w, p) for t, by_w in _LEXICON.items() for w, p in by_w.items()]
    # a reciter named anywhere is evidence of recitation (topic and format)
    groups += [(t, 1.0, _RECITERS) for t in ("topic:quran_recitation", "format:recitation")]
    for tag, weight, phrases in groups:
        for raw in phrases.split("|"):
            parts = raw.replace("^", " ^").replace("~", " ~").replace("<", " <").split()
            words_raw = [p for p in parts if p[0] not in "~^<"]
            if not words_raw:
                continue
            veto: frozenset[str] = frozenset()
            require: frozenset[str] = frozenset()
            not_after: frozenset[str] = frozenset()
            for p in parts:
                if p[0] == "~":
                    veto |= ctx[p[1:]]
                elif p[0] == "^":
                    require |= ctx[p[1:]]
                elif p[0] == "<":
                    not_after |= ctx[p[1:]]
            wildcard = words_raw[-1].endswith("*")
            words = tuple(fold(" ".join(words_raw).rstrip("*")).split())
            if not words:
                continue
            e = _Entry(tag, " ".join(words), words, weight, veto, require, not_after, wildcard)
            if wildcard and len(words) == 1:
                wild.append(e)
            else:
                index.setdefault(words[0], []).append(e)
    return index, wild


_INDEX, _WILD = _compile()

#: one text's hits: tag → (strength 0..1, matched phrases)
Hits = dict[str, tuple[float, tuple[str, ...]]]


def _raw_matches(toks: list[str]) -> list[tuple[_Entry, int]]:
    """Every lexicon entry matching at every position, before overlap resolution."""
    forms = [_token_forms(t) for t in toks]
    all_forms: set[str] = set().union(*forms) if forms else set()
    out: list[tuple[_Entry, int]] = []
    for i, fs in enumerate(forms):
        prev = toks[i - 1] if i else ""
        nxt = toks[i + 1] if i + 1 < len(toks) else ""
        cands: list[_Entry] = []
        for f in fs:
            cands.extend(_INDEX.get(f, ()))
        cands.extend(e for e in _WILD if any(f.startswith(e.words[0]) for f in fs))
        for e in cands:
            if e.veto and e.veto & all_forms:
                continue
            if e.require and not e.require & all_forms:
                continue
            if prev in _SURAH_WORDS and e.words[0] not in _SURAH_WORDS:
                continue
            if e.not_after and prev and _token_forms(prev) & e.not_after:
                continue
            if len(e.words) == 1 and (prev in _NAME_BEFORE or nxt in _NAME_AFTER):
                continue
            if _match_at(forms, i, e):
                out.append((e, i))
    return out


def _tokens(text: str, strip: Sequence[str]) -> list[str]:
    toks = fold(text).split()
    for s in strip:
        toks = _remove_phrase(toks, fold(s).split())
    return toks


def match_text(
    text: str, *, noise: frozenset[str] = frozenset(), strip: Sequence[str] = ()
) -> Hits:
    """Lexicon hits in one text.

    ``strip`` are phrases removed first (channel/presenter branding repeated
    in every title must not count as content); ``noise`` are lexicon phrases
    to ignore (channel boilerplate, see :func:`boilerplate_phrases`).

    Within a tag, overlapping phrases count once — the heaviest, then the
    longest (``السيرة النبوية`` over ``السيرة``; ``عمر الفاروق`` over
    ``الفاروق``). Strength is the sum of the kept phrases' weights, where
    hints (weight < 0.5) together add at most 0.35 — five weak words are
    still weak evidence — capped at 1.
    """
    toks = _tokens(text, strip)
    if not toks:
        return {}
    raw = _raw_matches(toks)
    noisy = {i for e, i in raw if e.phrase in noise}  # boilerplate spans, any tag
    by_tag: dict[str, list[tuple[_Entry, int]]] = {}
    for e, i in raw:
        if i not in noisy and e.phrase not in noise:
            by_tag.setdefault(e.tag, []).append((e, i))
    out: Hits = {}
    for tag, ms in by_tag.items():
        taken: list[tuple[int, int]] = []
        best: dict[str, float] = {}
        for e, i in sorted(ms, key=lambda m: (-m[0].weight, -len(m[0].words), m[1])):
            span = (i, i + len(e.words))
            if any(span[0] < b and a < span[1] for a, b in taken):
                continue
            taken.append(span)
            best[e.phrase] = max(e.weight, best.get(e.phrase, 0.0))
        strong = sum(w for w in best.values() if w >= _HINT)
        hints = sum(w for w in best.values() if w < _HINT)
        strength = min(1.0, strong + min(_HINT_CAP, hints))
        out[tag] = (strength, tuple(sorted(best, key=lambda p: -best[p])))
    out.update(_surah_title(toks))
    return out


# English transliterated surah names, compared as consonant skeletons
# ("Al-Waqi'a" / "Al-Waqia" → "wq"): mushaf rips are titled "Al-Baqara： 02",
# "104. Al Humaza： Der Stichler", "Surah Yasin".
_SURAH_EN = (
    "fatiha baqara imran nisa maida anam araf anfal tawba yunus hud yusuf rad ibrahim hijr "
    "nahl isra kahf maryam taha anbiya hajj muminun nur furqan shuara naml qasas ankabut rum "
    "luqman sajda ahzab saba fatir yasin saffat sad zumar ghafir fussilat shura zukhruf dukhan "
    "jathiya ahqaf muhammad fath hujurat qaf dhariyat tur najm qamar rahman waqia hadid "
    "mujadila hashr mumtahina saff jumua munafiqun taghabun talaq tahrim mulk qalam haqqa "
    "maarij nuh jinn muzzammil muddathir qiyama insan dahr mursalat naba naziat abasa takwir "
    "infitar mutaffifin inshiqaq buruj tariq ala ghashiya fajr balad shams layl duha sharh "
    "inshirah tin alaq qadr bayyina zalzala adiyat qaria takathur asr humaza fil quraysh "
    "quraish maun kawthar kafirun nasr masad lahab ikhlas falaq nas"
)
_ARTICLES = frozenset({"al", "an", "at", "as", "ash", "az", "adh", "ad", "ar", "aal"})
_SURAH_PREFIX = frozenset({"surah", "surat", "sura", "soorah"})


def _skeleton(word: str) -> str:
    s = "".join(c for c in word if c.isalpha() and c not in "aeiouy")
    return "".join(c for i, c in enumerate(s) if i == 0 or c != s[i - 1])


_SURAH_SKELETONS = frozenset(_skeleton(w) for w in _SURAH_EN.split())


def _surah_title(toks: list[str]) -> Hits:
    """A short Latin title that is only a surah name (+ its number) is a recitation track.

    Requires the surah word (``Surah …``) or a number 1–114, and at most
    three name words, so ``Episode 5`` or a lecture title never qualifies.
    """
    if not toks or len(toks) > 6 or not all(t.isascii() for t in toks):
        return {}
    nums = [int(t) for t in toks if t.isdigit()]
    words = [t for t in toks if not t.isdigit()]
    has_prefix = any(w in _SURAH_PREFIX for w in words)
    words = [w for w in words if w not in _SURAH_PREFIX and w not in _ARTICLES and len(w) > 1]
    if not words or len(words) > 3:
        return {}
    named = (
        _skeleton("".join(words[:2])) in _SURAH_SKELETONS or _skeleton(words[0]) in _SURAH_SKELETONS
    )
    if not named or not (has_prefix or any(1 <= n <= 114 for n in nums)):
        return {}
    return {
        "topic:quran_recitation": (1.0, ("surah title",)),
        "format:recitation": (1.0, ("surah title",)),
    }


def _match_at(forms: list[frozenset[str]], i: int, e: _Entry) -> bool:
    if i + len(e.words) > len(forms):
        return False
    for j, w in enumerate(e.words):
        fs = forms[i + j]
        last = j == len(e.words) - 1
        if w in fs or (last and e.wildcard and any(f.startswith(w) for f in fs)):
            continue
        return False
    return True


def _remove_phrase(toks: list[str], phrase: list[str]) -> list[str]:
    if not phrase or len(phrase) > len(toks):
        return toks
    out: list[str] = []
    i = 0
    while i < len(toks):
        if toks[i : i + len(phrase)] == phrase:
            i += len(phrase)
        else:
            out.append(toks[i])
            i += 1
    return out


def _desc_head(text: str, limit: int = 400) -> str:
    """The video-specific opening of a description.

    YouTube descriptions are a short blurb followed by pasted blocks: links
    to other programs, social accounts, hashtag lines, often a whole
    *other* video's description (``ما حكم عيد الحب`` under a Ramadan
    episode). Only the opening, up to the first link, separator line or
    blank line after some text, describes this video.
    """
    out: list[str] = []
    for line in (text or "").splitlines():
        stripped = line.strip()
        if "http" in stripped or stripped.startswith(("---", "___", "===", "***")):
            break
        if not stripped:
            if out:
                break
            continue
        out.append(stripped)
    return " ".join(out)[:limit]


def boilerplate_phrases(
    texts: Iterable[str], *, min_share: float = 0.35, min_count: int = 5
) -> frozenset[str]:
    """Lexicon phrases present in at least ``min_share`` of a channel's texts.

    Channels paste one footer under every upload (subscribe links, a
    hashtag line such as ``#السيرة_النبوية_نبيل_العوضي``). Phrases that
    common say nothing about one series; pass the result as ``noise``.
    """
    counts: Counter[str] = Counter()
    n = 0
    for t in texts:
        if not t:
            continue
        n += 1
        phrases = {e.phrase for e, _ in _raw_matches(_tokens(_desc_head(t), ()))}
        counts.update(phrases)
    if n < min_count:
        return frozenset()
    return frozenset(p for p, c in counts.items() if c >= min_count and c / n >= min_share)


# ---------------------------------------------------------------------------
# Hijri windows from Gregorian dates (1 Ramadan, Umm al-Qura / announced).
# Approximate by a day or two, which the window margins absorb.
# ---------------------------------------------------------------------------

_RAMADAN_START: dict[int, date] = {
    y: date.fromisoformat(d)
    for y, d in (
        (2005, "2005-10-04"), (2006, "2006-09-23"), (2007, "2007-09-13"), (2008, "2008-09-01"),
        (2009, "2009-08-22"), (2010, "2010-08-11"), (2011, "2011-08-01"), (2012, "2012-07-20"),
        (2013, "2013-07-09"), (2014, "2014-06-28"), (2015, "2015-06-18"), (2016, "2016-06-06"),
        (2017, "2017-05-27"), (2018, "2018-05-16"), (2019, "2019-05-06"), (2020, "2020-04-24"),
        (2021, "2021-04-13"), (2022, "2022-04-02"), (2023, "2023-03-23"), (2024, "2024-03-11"),
        (2025, "2025-03-01"), (2026, "2026-02-18"), (2027, "2027-02-08"), (2028, "2028-01-28"),
        (2029, "2029-01-16"), (2030, "2030-01-05"),
    )
}  # fmt: skip


def _in_ramadan(d: date) -> bool:
    # 2 days of slack before (moon sighting, eve uploads), 31 after (30 days + Eid eve)
    s = _RAMADAN_START.get(d.year)
    return bool(s and s - timedelta(days=2) <= d <= s + timedelta(days=31))


def _in_hajj_season(d: date) -> bool:
    # 1–13 Dhu al-Hijjah ≈ 1 Ramadan + 88 … 101 days
    for y in (d.year, d.year - 1):
        s = _RAMADAN_START.get(y)
        if s and s + timedelta(days=86) <= d <= s + timedelta(days=102):
            return True
    return False


# ---------------------------------------------------------------------------
# Scoring
# ---------------------------------------------------------------------------


class _Acc:
    """Raw evidence per tag, summed over sources."""

    def __init__(self) -> None:
        self.raw: dict[str, float] = {}
        self.ev: dict[str, list[str]] = {}

    def add(self, tag: str, amount: float, evidence: str) -> None:
        if amount <= 0:
            return
        self.raw[tag] = self.raw.get(tag, 0.0) + amount
        self.ev.setdefault(tag, []).append(evidence)

    def add_hits(self, source: str, hits: Hits, weight: float) -> None:
        for tag, (strength, phrases) in hits.items():
            self.add(tag, weight * strength, f"{source}:{'/'.join(phrases[:3])}")

    def add_share(self, source: str, per_text: Sequence[Hits], weight: float) -> None:
        """Share-weighted: mean strength over texts (0 for texts without a hit)."""
        n = len(per_text)
        if not n:
            return
        total: dict[str, float] = {}
        count: Counter[str] = Counter()
        phrases: dict[str, Counter[str]] = {}
        for hits in per_text:
            for tag, (strength, ps) in hits.items():
                total[tag] = total.get(tag, 0.0) + strength
                count[tag] += 1
                phrases.setdefault(tag, Counter()).update(ps)
        for tag, s in total.items():
            top = "/".join(p for p, _ in phrases[tag].most_common(2))
            self.add(tag, weight * s / n, f"{source} {count[tag]}/{n}:{top}")


_HONORIFICS = frozenset(
    fold(
        "الشيخ للشيخ شيخ الدكتور د دكتور لفضيلة فضيلة الإمام العلامة المهندس الأستاذ القارئ "
        "للقارئ القناة الرسمية قناة dr sheikh shaykh official channel"
    ).split()
)


def branding(channel: Sequence[str]) -> list[str]:
    """Phrases to strip from titles: the channel names and the presenter name in them.

    ``Dr. Othman Alkamees - الشيخ الدكتور عثمان الخميس`` → the whole name, each
    ``-``/``|`` segment, and each segment without honorifics (``عثمان الخميس``),
    longest first, so a title's ``| عثمان الخميس`` does not read as "Uthman".
    """
    out: set[str] = set()
    for c in channel:
        f = fold(c)
        if not f:
            continue
        out.add(f)
        for seg in re.split(r"\s[-|｜–—]\s|[()（）]", c):
            words = [w for w in fold(seg).split() if w not in _HONORIFICS]
            if len(words) >= 2 or (words and len(words[0]) >= 4 and not words[0].isdigit()):
                out.add(" ".join(words))
    return sorted(out, key=lambda x: -len(x))


def _channel_hits(channel: Sequence[str]) -> Hits:
    merged: Hits = {}
    for c in channel:
        for tag, (s, ps) in match_text(c).items():
            old = merged.get(tag)
            if not old or s > old[0]:
                merged[tag] = (s, ps)
    return merged


def _finish(acc: _Acc, cfg: TaggerConfig) -> list[Tag]:
    tags = [
        Tag(t, round(1.0 - math.exp(-raw), 3), "; ".join(acc.ev[t]))
        for t, raw in acc.raw.items()
        if 1.0 - math.exp(-raw) >= cfg.min_score
    ]
    if not any(t.tag.startswith("audience:") for t in tags):
        tags.append(Tag("audience:general", 0.5, "default: no narrower audience evidenced"))
    order = {f"topic:{t}": i for i, t in enumerate(TOPICS)}
    tags.sort(key=lambda t: (NAMESPACES.index(t.tag.split(":")[0]), -t.score, order.get(t.tag, 99)))
    top = primary_topic(tags)
    if top:
        tags.append(Tag("genre:" + top.tag.split(":", 1)[1], top.score, "primary topic"))
    else:
        # smart-mix needs a genre for every item; "general" keeps it schedulable
        tags.append(Tag("genre:general", 0.0, "no topic evidence"))
    return tags


_TALK_FORMATS = frozenset({"format:lecture", "format:program", "format:qa_fatwa"})


def _resolve_conflicts(acc: _Acc) -> None:
    """Rules where one reading excludes another.

    - Quran recited vs Quran explained: when explanation evidence clearly
      dominates, drop recitation (``سورة`` in a tafsir title was vetoed per
      title already; this catches series-level labels).
    - Clip series (detector ``clips``) are not ``format:program`` even though
      every clip title names its program.
    - A khutbah is never a recitation, whatever surah it is about.
    - A reciter's nasheed is a nasheed, not a recitation.
    - Nasheeds sing about the Prophet and the Companions: their topic words
      are lyrics, so with strong nasheed evidence the other topics (and talk
      formats) are damped and ``nasheed`` becomes the genre.
    """
    raw = acc.raw
    rec, taf = raw.get("topic:quran_recitation", 0.0), raw.get("topic:tafsir", 0.0)
    if rec and taf > 2 * rec:
        raw.pop("topic:quran_recitation", None)
        raw.pop("format:recitation", None)
    if raw.get("format:clips", 0.0) >= 3.0:
        # highlight clips quote their program's name; they are not the program
        raw.pop("format:program", None)
    if raw.get("format:khutbah", 0.0):
        # a khutbah is preaching: a surah it is about, or verses it quotes, never make
        # it a recitation
        raw.pop("topic:quran_recitation", None)
        raw.pop("format:recitation", None)
    nas = raw.get("topic:nasheed", 0.0)
    if nas and nas >= raw.get("topic:quran_recitation", 0.0):
        # reciters also sing nasheeds (العفاسي): the nasheed word decides
        raw.pop("topic:quran_recitation", None)
        raw.pop("format:recitation", None)
    if nas >= 2.0:
        # a nasheed about the Prophet or a Companion is programmed as a nasheed
        for t in list(raw):
            if t != "topic:nasheed" and (t.startswith("topic:") or t in _TALK_FORMATS):
                raw[t] *= 0.4


def tag_series(
    name: str,
    *,
    playlist_titles: Sequence[str] = (),
    episode_titles: Sequence[str] = (),
    descriptions: Sequence[str] = (),
    channel: Sequence[str] = (),
    median_duration_s: float | None = None,
    kind: str | None = None,
    air_dates: Sequence[date] = (),
    noise: frozenset[str] = frozenset(),
    config: TaggerConfig | None = None,
) -> list[Tag]:
    """Tags for one series (season): ``[(tag, score, evidence), …]``.

    ``channel`` is the channel title and/or folder (presenter) name; its
    phrases are stripped from the other texts as branding and used once as
    a prior. ``air_dates`` should hold *exact* upload dates only (Ramadan /
    Hajj detection). ``kind`` is the detector's (``episodic`` | ``clips``).
    The ``genre:`` tag repeats the primary topic; see :func:`primary_topic`.
    """
    cfg = config or TaggerConfig()
    acc = _Acc()
    strip = branding(channel)
    acc.add_hits("name", match_text(name, strip=strip), cfg.w_name)
    best: Hits = {}
    for pl in playlist_titles:
        for tag, (s, ps) in match_text(pl, strip=strip).items():
            if tag not in best or s > best[tag][0]:
                best[tag] = (s, ps)
    acc.add_hits("playlist", best, cfg.w_playlist)
    acc.add_share("eps", [match_text(t, strip=strip) for t in episode_titles], cfg.w_episodes)
    acc.add_share(
        "desc",
        [match_text(_desc_head(d), noise=noise, strip=strip) for d in descriptions if d],
        cfg.w_description,
    )
    acc.add_hits("channel", _channel_hits(channel), cfg.w_channel)

    if kind == "clips":
        acc.add("format:clips", 3.0, "detector:clips")
    if median_duration_s is not None and median_duration_s < cfg.clip_max_s:
        acc.add("format:clips", 0.8, f"median {median_duration_s / 60:.1f} min")
    dated = sorted(air_dates)
    if len(dated) >= cfg.min_dated_episodes:
        n = len(dated)
        ram = sum(_in_ramadan(d) for d in dated)
        if ram / n >= cfg.occasion_share:
            acc.add("occasion:ramadan", 2.5, f"aired in Ramadan {ram}/{n}")
        hajj = sum(_in_hajj_season(d) for d in dated)
        if hajj / n >= cfg.occasion_share:
            acc.add("occasion:hajj", 2.0, f"aired in Dhu al-Hijjah {hajj}/{n}")
        if (
            n >= cfg.program_min_episodes
            and kind != "clips"
            and _densest_window(dated, cfg.program_window_days) >= cfg.program_min_episodes
        ):
            acc.add("format:program", 1.0, f"broadcast run: {n} episodes in a season")
    _default_format(acc, median_duration_s, cfg)
    _resolve_conflicts(acc)
    return _finish(acc, cfg)


def _densest_window(dated: Sequence[date], days: int) -> int:
    best = j = 0
    for i, d in enumerate(dated):
        while dated[j] < d - timedelta(days=days):
            j += 1
        best = max(best, i - j + 1)
    return best


def _default_format(acc: _Acc, duration_s: float | None, cfg: TaggerConfig) -> None:
    if duration_s is None:
        return
    has_format = any(
        t.startswith("format:") and 1.0 - math.exp(-r) >= cfg.min_score for t, r in acc.raw.items()
    )
    if not has_format and duration_s >= cfg.long_form_s:
        acc.add("format:lecture", 0.6, f"default: long-form {duration_s / 60:.0f} min")


def tag_video(
    title: str,
    description: str = "",
    duration_s: float | None = None,
    *,
    channel: Sequence[str] = (),
    is_short: bool = False,
    noise: frozenset[str] = frozenset(),
    config: TaggerConfig | None = None,
) -> list[Tag]:
    """Tags for one stand-alone video (filler / short / unassigned)."""
    cfg = config or TaggerConfig()
    acc = _Acc()
    strip = branding(channel)
    acc.add_hits("title", match_text(title, strip=strip), cfg.w_title)
    if description:
        acc.add_hits(
            "desc", match_text(_desc_head(description), noise=noise, strip=strip), cfg.w_description
        )
    acc.add_hits("channel", _channel_hits(channel), cfg.w_channel)
    if is_short:
        acc.add("format:clips", 3.0, "short")
    elif duration_s is not None and duration_s < cfg.clip_max_s:
        acc.add("format:clips", 1.0, f"{duration_s / 60:.1f} min")
    _default_format(acc, duration_s, cfg)
    _resolve_conflicts(acc)
    return _finish(acc, cfg)


def primary_topic(tags: Iterable[Tag]) -> Tag | None:
    """The highest-scoring ``topic:`` tag of :data:`TOPICS` (ties → its order); a
    :data:`SUBTOPICS` tag never is: its broad topic is the genre."""
    order = {f"topic:{t}": i for i, t in enumerate(TOPICS)}
    topics = [t for t in tags if t.tag in order]
    if not topics:
        return None
    return min(topics, key=lambda t: (-t.score, order.get(t.tag, 99)))


def content_type(tags: Iterable[Tag]) -> str:
    """Story 26.2 ``video_classification.content_type`` for these tags.

    Nasheed → ``music_video``; otherwise the strongest format mapped by
    :data:`FORMAT_CONTENT_TYPE`, skipping ``clips`` (a clip of a lecture is
    still a lecture). ``unknown`` when nothing maps.
    """
    ts = list(tags)
    top = primary_topic(ts)
    if top and top.tag == "topic:nasheed":
        return "music_video"
    formats = sorted(
        (t for t in ts if t.tag.startswith("format:") and t.tag != "format:clips"),
        key=lambda t: -t.score,
    )
    for t in formats:
        ct = FORMAT_CONTENT_TYPE.get(t.tag.split(":", 1)[1], "unknown")
        if ct != "unknown":
            return ct
    return "unknown"
