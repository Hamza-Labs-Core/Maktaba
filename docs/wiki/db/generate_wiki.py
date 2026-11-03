#!/usr/bin/env python3
"""Generate the Maktaba wiki database from the repo's own specs.

    python3 docs/wiki/db/generate_wiki.py           write the database
    python3 docs/wiki/db/generate_wiki.py --check   fail if the committed database is stale

Reads (all repo-relative, found from this file's own path, so it runs from any directory):
  specs/epics/*/            each epic's README.md, story-*.md and plan-*.md; an epic without
                            story files gets its stories from the README's Stories section
  specs/*.md                architecture.md (DB tables) and the review / audit documents
  specs/diagrams/*.drawio
  shared/api/openapi.yaml   the endpoints
  web/mockups/**/*.html
  docs/wiki/db/curated.json the hand-kept data the specs do not carry (epic names, phases
                            and engines, hand-written summaries, the implementation
                            phases, the cross-reference inventory). Edit it, not the output.
  docs/wiki/db/ar/*.json    the Arabic of every entry's text (the translation store, see
                            "Arabic" below)
  shared/api/openapi.ar.json the Arabic of the endpoints' summaries, where it has them

Writes:
  web/wiki-app/public/db/wiki.json              meta, its parts, and every entry that is not
                                                a story or a plan
  web/wiki-app/public/db/wiki-epics-NN-MM.json  the stories and plans of epics NN to MM
  web/wiki-app/public/db/wiki-cross-refs.json   the cross-reference inventory
  web/wiki-app/public/db/ar/*.json              the same files in Arabic (maktaba.dev/ar/wiki/)
  docs/wiki/db/wiki-schema.json                 the JSON schema

Arabic: every entry's title and content (and an epic's engine) has its Modern Standard Arabic
in the translation store, docs/wiki/db/ar/: epic-NN.json for Epic NN, its stories and plans,
and common.json for the rest. Each text is keyed by entry id and field, and records `hash`,
the first 16 hex digits of the SHA-256 of the English it translates. --check fails when an
English text has no Arabic, when its English changed since it was translated, when the
Arabic dropped a piece of code, or when it is mostly Latin prose; each finding names the
file, the entry and the field. To see what needs translating, with the English and the hash
to record:

    python3 docs/wiki/db/generate_wiki.py --ar-todo [--epic NN]

The output is deterministic (no timestamps), so --check compares bytes. CI runs --check
(.github/workflows/ci.yml, the wiki job) whenever a spec, a mockup, the OpenAPI description or
this folder changes. Every string is scrubbed of home paths, private addresses and
personal .local names, and no paragraph quotes the owner.

Python 3.10+, standard library only.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import wiki_ar  # noqa: E402

REPO = Path(__file__).resolve().parents[3]
SPECS = REPO / "specs"
CURATED = REPO / "docs" / "wiki" / "db" / "curated.json"
OUT_DB = REPO / "web" / "wiki-app" / "public" / "db"
SCHEMA_OUT = REPO / "docs" / "wiki" / "db" / "wiki-schema.json"
AR_STORE = REPO / "docs" / "wiki" / "db" / "ar"
OUT_DB_AR = OUT_DB / "ar"
OPENAPI_AR = REPO / "shared" / "api" / "openapi.ar.json"

# Epics per wiki-epics-NN-MM.json part file.
PART_SIZE = 9
# The phase an epic takes when curated.json names none: "Platform", Epics 25 onward.
DEFAULT_PHASE = 5
# Characters of a spec file kept as an entry's content.
EXCERPT = 600

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def read(p: Path) -> str:
    try:
        return p.read_text(encoding="utf-8")
    except OSError:
        return ""


def rel(p: Path) -> str:
    return p.relative_to(REPO).as_posix()


def nat_key(s: str) -> list[tuple[int, int | str]]:
    """Natural sort: 'story-9.2' < 'story-10.1'; numbers and words never compared."""
    return [(0, int(t)) if t.isdigit() else (1, t) for t in re.findall(r"\d+|\D+", s)]


def slug_to_title(slug: str) -> str:
    return slug.replace("-", " ").title()


def slugify(s: str) -> str:
    return re.sub(r"[^a-z0-9]+", "-", s.lower()).strip("-")[:60]


def first_heading(text: str) -> str:
    for line in text.splitlines():
        if line.startswith("# "):
            return line[2:].strip()
    return ""


def parse_doc_name(prefix: str, filename: str):
    """story-EE-NN-slug.md -> (EE, NN, slug); story-EE-slug.md -> (EE, None, slug)."""
    m = re.match(rf"^{prefix}-(\d{{2}})-(?:(\d{{2}})-)?(.+)\.md$", filename)
    if not m:
        return None
    return int(m.group(1)), (int(m.group(2)) if m.group(2) else None), m.group(3)


def dotted(ee: int, nn: int | None, slug: str) -> str:
    return f"{ee}.{nn}" if nn is not None else f"{ee}.{slug}"


def strip_title(h: str, word: str) -> str:
    return re.sub(rf"^{word}\s+\d+\.\d+\s*[—–:-]\s*", "", h).strip()


def epic_name_from_readme(readme: str, fallback: str) -> str:
    """'# Epic 50: Maktaba Cloud with no downtime: from ...' -> 'Maktaba Cloud with no downtime'."""
    h = first_heading(readme)
    h = re.sub(r"^Epic\s+\d+\s*[—–:-]\s*", "", h).strip()
    return h.split(": ")[0].strip() or fallback


def epic_goal(readme: str) -> str:
    """The **Goal.** paragraph, else the first block under '## Goal(s)'. Without either: the
    title's own words, then the first plain paragraph that states something (not a quote's
    lead-in ending in ':', not a heading, blockquote, table or list)."""
    lines = readme.splitlines()

    def paragraph(start: int) -> str:
        out = []
        for line in lines[start:]:
            if not line.strip() or line.startswith("#"):
                break
            out.append(line.strip())
        return " ".join(out)

    for i, line in enumerate(lines):
        if line.strip().startswith("**Goal.**"):
            return paragraph(i).replace("**Goal.**", "").strip()
    title = re.sub(r"^Epic\s+\d+\s*[—–:-]\s*", "", first_heading(readme)).strip()
    for i, line in enumerate(lines):
        if re.match(r"^##\s+Goals?\b", line):
            for j in range(i + 1, len(lines)):
                if lines[j].startswith("#"):
                    break
                if lines[j].strip() and not lines[j - 1].strip():
                    p = paragraph(j)
                    if not p.endswith(":") and not p.startswith(">"):
                        return f"{title}.\n\n{p}" if title else p
    for i, line in enumerate(lines):
        s = line.strip()
        if not s or s.startswith(("#", ">", "|", "-", "*", "```")) or re.match(r"^\d+\.", s):
            continue
        if i > 0 and lines[i - 1].strip():
            continue
        p = paragraph(i)
        if p.endswith(":"):
            continue
        return f"{title}.\n\n{p}" if title else p
    return title


# ---- inline stories (an epic whose README lists its stories) -----------------------------

STORY_HEADING = re.compile(r"^(#{1,6})\s+.*\bstor(y|ies)\b", re.IGNORECASE)


def inline_stories(readme: str, ee: int) -> list[dict]:
    """Stories an epic README lists in its Stories section(s): table rows whose first cell is
    the story number ('| 45.1 | **Title** | scope |') and list items that start with it in
    bold ('- **45.1** text', '1. **42.1 Title:** text'). First mention wins."""
    out: dict[str, dict] = {}
    level = 0
    item: dict | None = None  # the list item a wrapped line continues
    for line in readme.splitlines():
        if item is not None and re.match(r"^\s{2,}\S", line) and not re.match(r"^\s*(?:[-*]|\d+\.)\s", line):
            item["text"] += " " + line.strip()
            continue
        item = None
        h = re.match(r"^(#{1,6})\s", line)
        if h:
            n = len(h.group(1))
            if STORY_HEADING.match(line):
                level = n
            elif level and n <= level:
                level = 0
            continue
        if not level:
            continue
        m = re.match(rf"^\|\s*\**{ee}\.(\d+)\**\s*\|\s*(.*?)\s*\|(.*)$", line)
        if m:
            nn, title, rest = int(m.group(1)), m.group(2), m.group(3)
            title = title.replace("**", "").strip()
            scope = " ".join(c.strip() for c in rest.split("|") if c.strip())
            if not title:
                title = scope.split(". ")[0]
            text = f"{title}. {scope}".strip() if scope else title
            out.setdefault(f"{ee}.{nn}", {"nn": nn, "title": short_title(title), "text": text})
            continue
        m = re.match(rf"^\s*(?:[-*]|\d+\.)\s+\*\*{ee}\.(\d+)\b\s*([^*]*)\*\*[\s:.—–-]*(.*)$", line)
        if m:
            nn, bold, rest = int(m.group(1)), m.group(2).strip().rstrip(":.").strip(), m.group(3).strip()
            text = f"{bold}. {rest}" if bold else rest
            key = f"{ee}.{nn}"
            if key not in out:
                out[key] = {"nn": nn, "title": bold, "text": text}
                item = out[key]
    for v in out.values():
        if not v["title"]:  # a list item: its first clause
            v["title"] = re.split(r"(?<=[.;:])\s|\s[—–]\s", v["text"])[0].rstrip(".;:")
        v["title"] = short_title(v["title"])
    return [dict(v, id=k) for k, v in out.items()]


def short_title(t: str, limit: int = 90) -> str:
    """A title cut to its first clause, or at a word, when it runs past `limit`."""
    t = t.strip()
    if len(t) <= limit:
        return t
    for sep in ("; ", ": ", " (", ", "):
        i = t.find(sep)
        if 8 <= i <= limit:
            return t[:i]
    return t[: t.rfind(" ", 0, limit)].rstrip(",;:") + "…"


# ---- no quotes of the owner's words ------------------------------------------------------

OWNER = re.compile(r"\b(?:the\s+)?owner(?:'s|’s)?\b", re.IGNORECASE)
QUOTE = re.compile(r"[\"“”]")


def drop_owner_quotes(text: str) -> str:
    """The wiki never quotes the owner: a paragraph that names the owner and quotes them
    goes, and so does a lead-in ('The owner's decisions (2026-10-19):') with the quote or
    list that follows it, up to the next heading."""
    out: list[str] = []
    skipping = False
    for block in re.split(r"\n\s*\n", text):
        s = block.strip()
        if s.startswith("#"):
            skipping = False
        elif skipping:
            continue
        if OWNER.search(s) and not s.startswith("#"):
            if QUOTE.search(s):
                continue
            if s.endswith(":"):
                skipping = True
                continue
        out.append(block)
    return "\n\n".join(out)


# ---- public safety (the same rules as web/packages/devportal/scripts/scan.mjs) ----------

ALLOWED_LOCAL_HOSTS = {"maktaba.local", "tcp.local", "_tcp.local"}
SCRUB = [
    (re.compile(r"/Users/[A-Za-z0-9._-]+"), "~"),
    (re.compile(r"/home/[a-z][a-z0-9_-]*/(?!\*)"), "~/"),
    (re.compile(r"C:\\Users\\[^\\\s]*"), "%USERPROFILE%"),
    (re.compile(r"claude-trail", re.IGNORECASE), "[internal]"),
    (re.compile(r"docs/audits/"), "[internal]/"),
    (
        re.compile(
            r"(?<![\d.])(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}"
            r"|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}"
            r"|100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3}"
            r"|169\.254\.\d{1,3}\.\d{1,3})(?![\d.]*\d)"
        ),
        "[private address]",
    ),
    (re.compile(r"\b[a-z0-9-]+\.[a-z0-9-]+\.ts\.net\b", re.IGNORECASE), "[name].ts.net"),
]
LOCAL_HOST = re.compile(r"\b[A-Za-z0-9_][A-Za-z0-9_-]*\.local\b")


def scrub_text(s: str) -> str:
    for rx, rep in SCRUB:
        s = rx.sub(rep, s)
    return LOCAL_HOST.sub(lambda m: m.group(0) if m.group(0).lower() in ALLOWED_LOCAL_HOSTS else "[name].local", s)


def scrub(v):
    if isinstance(v, str):
        return scrub_text(v)
    if isinstance(v, list):
        return [scrub(x) for x in v]
    if isinstance(v, dict):
        return {k: scrub(x) for k, x in v.items()}
    return v


# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

DESCRIPTION = ("Self-hosted media intelligence platform: full Plex alternative + "
               "every-word-searchable transcripts. See specs/architecture.md.")


class Built:
    """The files to write (path -> text), and the Arabic drift check's findings and to-do."""

    def __init__(self, files: dict[Path, str], ar: wiki_ar.Translated) -> None:
        self.files = files
        self.findings = ar.findings
        self.todo = ar.todo
        self.translated = ar.translated
        self.total = len(ar.entries)


def build_all() -> Built:
    curated = json.loads(read(CURATED) or "{}")
    cur_epics: dict = curated.get("epics", {})
    overlays: dict = curated.get("overlays", {})

    # ---- 1. epics, stories, plans --------------------------------------------------------
    epics: dict[str, dict] = {}
    stories: dict[str, dict] = {}
    plans: dict[str, dict] = {}

    for epic_dir in sorted((SPECS / "epics").iterdir(), key=lambda p: nat_key(p.name)):
        if not epic_dir.is_dir() or not re.match(r"^\d{2}-", epic_dir.name):
            continue
        en = epic_dir.name
        ee = int(en[:2])
        readme = drop_owner_quotes(read(epic_dir / "README.md"))
        meta = cur_epics.get(en, {})
        name = meta.get("name") or epic_name_from_readme(readme, slug_to_title(en[3:]))
        phase = meta.get("phase", DEFAULT_PHASE)
        epic_stories: list[str] = []
        epic_plans: list[str] = []
        for f in sorted(epic_dir.iterdir(), key=lambda p: nat_key(p.name)):
            if f.name.startswith("story-"):
                p = parse_doc_name("story", f.name)
                if not p:
                    continue
                s_ee, nn, slug = p
                sid = dotted(s_ee, nn, slug)
                text = drop_owner_quotes(read(f))
                stories[sid] = {
                    "id": sid, "nn": nn, "slug": slug, "epic": en, "phase": phase,
                    "title": strip_title(first_heading(text), "Story") or slug_to_title(slug),
                    "story_file": rel(f), "plan_file": None, "plan_key": None, "raw": text,
                }
                epic_stories.append(sid)
            elif f.name.startswith("plan-"):
                p = parse_doc_name("plan", f.name)
                if not p:
                    continue
                p_ee, nn, slug = p
                key = f"{p_ee:02d}.{nn:02d}-{slug}" if nn is not None else f"{p_ee:02d}-{slug}"
                text = drop_owner_quotes(read(f))
                plans[key] = {
                    "id": key, "ee": p_ee, "nn": nn, "slug": slug, "epic": en, "phase": phase,
                    "title": strip_title(first_heading(text), "Plan") or slug_to_title(slug),
                    "plan_file": rel(f), "raw": text,
                }
                epic_plans.append(key)
        for st in inline_stories(readme, ee):
            if st["id"] in stories:
                continue
            stories[st["id"]] = {
                "id": st["id"], "nn": st["nn"], "slug": slugify(st["title"]), "epic": en,
                "phase": phase, "title": st["title"], "story_file": rel(epic_dir / "README.md"),
                "plan_file": None, "plan_key": None, "raw": st["text"], "inline": True,
            }
            epic_stories.append(st["id"])
        epics[en] = {
            "id": en, "num": ee, "name": name, "phase": phase, "engine": meta.get("engine"),
            "goal": epic_goal(readme), "readme": rel(epic_dir / "README.md"),
            "stories": sorted(epic_stories, key=nat_key), "plans": epic_plans,
        }

    for plan in plans.values():
        sid = dotted(plan["ee"], plan["nn"], plan["slug"])
        st = stories.get(sid)
        if st and (not st["plan_file"] or st["slug"] == plan["slug"]):
            st["plan_file"] = plan["plan_file"]
            st["plan_key"] = plan["id"]

    # ---- 2. diagrams, reviews, mockups ---------------------------------------------------
    diagrams = [
        {"id": f"diagram-{d.stem}", "name": d.stem, "title": slug_to_title(d.stem), "file": rel(d)}
        for d in sorted((SPECS / "diagrams").glob("*.drawio"))
    ]
    reviews = [
        {"id": f"review-{r.stem.lower()}", "name": r.stem, "file": rel(r)}
        for r in sorted(SPECS.glob("*.md"))
        if r.name != "architecture.md"
    ]
    mockups = []
    mockup_ids: set[str] = set()
    for m in sorted((REPO / "web" / "mockups").rglob("*.html"), key=lambda p: nat_key(rel(p))):
        sm = re.match(r"^mockup-(\d{2})-(\d{2})-", m.stem)
        category = m.parent.name if m.parent.name != "mockups" else "shared"
        mid = f"mockup-{m.stem}"
        if mid in mockup_ids:  # the same name in another folder
            mid = f"mockup-{category}-{m.stem}"
        mockup_ids.add(mid)
        mockups.append({
            "id": mid, "name": m.stem, "title": slug_to_title(m.stem), "file": rel(m),
            "story": f"{int(sm.group(1))}.{int(sm.group(2))}" if sm else None, "category": category,
        })
    mockup_by_file = {mk["file"]: mk["id"] for mk in mockups}
    for mk in mockups:
        if mk["story"] in stories:
            stories[mk["story"]].setdefault("mockup_files", []).append(mk["file"])
    epic_mockups: dict[str, int] = defaultdict(int)
    category_epic = {"admin": "09-library-management", "desktop": "13-desktop", "mobile": "12-mobile",
                     "tv": "14-tv-apps", "theme-library": "17-ux-design-system"}
    for mk in mockups:
        if mk["story"]:
            ee = mk["story"].split(".")[0].zfill(2)
            for en in epics:
                if en.startswith(ee + "-"):
                    epic_mockups[en] += 1
                    break
        elif mk["category"] in category_epic:
            epic_mockups[category_epic[mk["category"]]] += 1

    # ---- 3. endpoints (a light walk of openapi.yaml's paths) -----------------------------
    endpoints = []
    lines = read(REPO / "shared" / "api" / "openapi.yaml").splitlines()
    in_paths, path, cur = False, None, None
    for line in lines:
        if line.startswith("paths:"):
            in_paths = True
            continue
        if not in_paths:
            continue
        if line and not line.startswith((" ", "#")):
            break
        mp = re.match(r"^  (/[A-Za-z0-9._/{}-]+):\s*$", line)
        if mp:
            path, cur = mp.group(1), None
            continue
        mm = re.match(r"^    (get|post|put|patch|delete|options|head):\s*$", line)
        if mm and path:
            method = mm.group(1).upper()
            cur = {
                "id": f"ep-{method.lower()}-{re.sub(r'[^A-Za-z0-9]+', '-', path).strip('-')}",
                "method": method, "path": path, "summary": "", "operationId": "", "tag": "",
                "_tags_next": False,
            }
            endpoints.append(cur)
            continue
        if cur is None:
            continue
        if re.match(r"^    \S", line):  # another key of the path item (parameters, …)
            cur = None
            continue
        ms = re.match(r"^      summary:\s*(.+)$", line)
        if ms:
            cur["summary"] = ms.group(1).strip().strip("\"'")
        mo = re.match(r"^      operationId:\s*(.+)$", line)
        if mo:
            cur["operationId"] = mo.group(1).strip().strip("\"'")
        mt = re.match(r"^      tags:\s*\[([^\]]*)\]", line)
        if mt and not cur["tag"]:
            cur["tag"] = mt.group(1).split(",")[0].strip().strip("\"'")
        if re.match(r"^      tags:\s*$", line):
            cur["_tags_next"] = True
            continue
        if cur["_tags_next"]:
            ml = re.match(r"^      (?:  )?- (.+)$", line)
            if ml and not cur["tag"]:
                cur["tag"] = ml.group(1).strip().strip("\"'")
            cur["_tags_next"] = False
    for ep in endpoints:
        ep.pop("_tags_next", None)

    # ---- 4. DB entities (architecture §8) ------------------------------------------------
    arch = read(SPECS / "architecture.md")
    entities: dict[str, dict] = {}
    for m in re.finditer(r"CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+(\w+)\s*\(", arch):
        entities.setdefault(m.group(1), {"name": m.group(1), "source": "architecture.md §8",
                                         "migration_slot": None, "owning_plan": None})
    m87 = re.search(r"\*\*Plan-introduced tables\.\*\*[\s\S]*?\|-+\|[\s\S]*?(?=\n\n)", arch)
    if m87:
        for name, plan_cell, slot in re.findall(
            r"^\|\s*`?(\w+)`?\s*\|\s*([^|]+)\|\s*([0-9]{4})\s*\|", m87.group(0), re.MULTILINE
        ):
            ent = entities.setdefault(name, {"name": name, "source": "architecture.md §8.7"})
            ent["migration_slot"] = slot
            ent["owning_plan"] = plan_cell.strip().strip("`")

    # ---- 5. cross-references by mention ---------------------------------------------------
    story_tables: dict[str, list[str]] = defaultdict(list)
    table_stories: dict[str, list[str]] = defaultdict(list)
    for sid in sorted(stories, key=nat_key):
        raw = stories[sid]["raw"]
        for ent in sorted(entities):
            if f"`{ent}`" in raw:
                story_tables[sid].append(ent)
                table_stories[ent].append(sid)
    story_eps: dict[str, list[str]] = defaultdict(list)
    ep_stories: dict[str, list[str]] = defaultdict(list)
    for ep in endpoints:
        for sid in sorted(stories, key=nat_key):
            if ep["path"] in stories[sid]["raw"]:
                story_eps[sid].append(f"{ep['method']} {ep['path']}")
                ep_stories[ep["id"]].append(sid)

    # ---- 6. entries -----------------------------------------------------------------------
    entries: list[dict] = []
    for en, e in epics.items():
        entries.append({
            "id": f"epic-{en}", "type": "epic", "title": e["name"], "epic": en,
            "phase": e["phase"], "engine": e["engine"], "content": e["goal"],
            "tags": ["epic", en, f"phase-{e['phase']}"],
            "related": [f"story-{s}" for s in e["stories"]] + [f"plan-{p}" for p in e["plans"]],
            "files": {"readme": e["readme"]},
            "metadata": {"story_count": len(e["stories"]), "plan_count": len(e["plans"]),
                         "mockup_count": epic_mockups.get(en, 0)},
        })
    for sid in sorted(stories, key=nat_key):
        st = stories[sid]
        mks = st.get("mockup_files", [])
        related = [f"epic-{st['epic']}"]
        if st["plan_key"]:
            related.append(f"plan-{st['plan_key']}")
        related += [f"entity-{t}" for t in story_tables.get(sid, [])]
        related += [ep["id"] for ep in endpoints if sid in ep_stories.get(ep["id"], [])]
        related += [mockup_by_file[m] for m in mks]
        meta = {"slug": st["slug"]}
        if st.get("inline"):
            meta["inline"] = True
        entries.append({
            "id": f"story-{sid}", "type": "story", "title": st["title"], "epic": st["epic"],
            "phase": st["phase"], "content": st["raw"][:EXCERPT],
            "tags": ["story", st["epic"], f"phase-{st['phase']}"], "related": related,
            "files": {"story": st["story_file"], "plan": st["plan_file"], "mockups": mks},
            "api_endpoints": story_eps.get(sid, []), "db_tables": story_tables.get(sid, []),
            "metadata": meta,
        })
    for pid in sorted(plans, key=nat_key):
        plan = plans[pid]
        related = [f"epic-{plan['epic']}"]
        sid = dotted(plan["ee"], plan["nn"], plan["slug"])
        if sid in stories:
            related.append(f"story-{sid}")
        entries.append({
            "id": f"plan-{pid}", "type": "plan", "title": plan["title"], "epic": plan["epic"],
            "phase": plan["phase"], "content": plan["raw"][:EXCERPT], "tags": ["plan", plan["epic"]],
            "related": related, "files": {"plan": plan["plan_file"]}, "metadata": {"slug": plan["slug"]},
        })
    for d in diagrams:
        entries.append({
            "id": d["id"], "type": "diagram", "title": d["title"],
            "content": f"Architecture diagram: {d['title']}.", "tags": ["diagram"], "related": [],
            "files": {"diagram": d["file"]},
        })
    for r in reviews:
        entries.append({
            "id": r["id"], "type": "review", "title": r["name"],
            "content": f"Review document: {r['name']}.", "tags": ["review"], "related": [],
            "files": {"review": r["file"]},
        })
    for mk in mockups:
        entries.append({
            "id": mk["id"], "type": "mockup", "title": mk["title"],
            "content": f"HTML mockup ({mk['category']}): {mk['title']}.",
            "tags": ["mockup", mk["category"]],
            "related": [f"story-{mk['story']}"] if mk["story"] in stories else [],
            "files": {"mockup": mk["file"]}, "metadata": {"category": mk["category"]},
        })
    for ep in endpoints:
        entries.append({
            "id": ep["id"], "type": "endpoint", "title": f"{ep['method']} {ep['path']}",
            "content": ep["summary"] or f"{ep['method']} {ep['path']}",
            "tags": ["api", ep["tag"] or "untagged", ep["method"].lower()],
            "related": [f"story-{s}" for s in ep_stories.get(ep["id"], [])],
            "files": {"openapi": "shared/api/openapi.yaml"},
            "metadata": {"method": ep["method"], "path": ep["path"], "tag": ep["tag"],
                         "operationId": ep["operationId"]},
        })
    for name in sorted(entities):
        ent = entities[name]
        entries.append({
            "id": f"entity-{name}", "type": "entity", "title": name,
            "content": f"Database table `{name}`. Source: {ent['source']}.",
            "tags": ["entity", "database"], "related": [f"story-{s}" for s in table_stories.get(name, [])],
            "files": {"schema": "specs/architecture.md#8-database-schema",
                      "manifest": "shared/db/migrations/MANIFEST.md"},
            "metadata": {"source": ent["source"], "migration_slot": ent.get("migration_slot"),
                         "owning_plan": ent.get("owning_plan")},
        })

    # ---- 7. the hand-kept data -------------------------------------------------------------
    by_id = {e["id"]: e for e in entries}
    for e in curated.get("entries", []):
        if e["id"] not in by_id:
            entries.append(e)
            by_id[e["id"]] = e
    for oid, o in overlays.items():
        e = by_id.get(oid)
        if e is None:
            continue  # the spec it described is gone
        summary = o.get("summary")
        if summary and summary not in (e.get("content") or ""):
            e["content"] = f"{summary}\n\n{e['content']}" if e.get("content") else summary
        for k in ("tags", "related", "api_endpoints", "db_tables"):
            if o.get(k):
                e[k] = list(dict.fromkeys((e.get(k) or []) + o[k]))
        if o.get("metadata"):
            e["metadata"] = {**o["metadata"], **(e.get("metadata") or {})}
    # a related id points at an entry that exists
    for e in entries:
        e["related"] = [r for r in e.get("related", []) if r in by_id and r != e["id"]]
    entries = scrub(entries)

    # ---- 8. the files ----------------------------------------------------------------------
    def dump(obj) -> str:
        return json.dumps(obj, indent=2, ensure_ascii=False) + "\n"

    max_epic = max((e["num"] for e in epics.values()), default=1)
    ranges = [(lo, min(lo + PART_SIZE - 1, max_epic)) for lo in range(1, max_epic + 1, PART_SIZE)]

    def part_name(lo: int, hi: int) -> str:
        return f"wiki-epics-{lo:02d}-{hi:02d}.json"

    parts: dict[str, list[dict]] = {part_name(lo, hi): [] for lo, hi in ranges}
    main_entries = []
    for e in entries:
        if e["type"] in ("story", "plan") and e.get("epic"):
            n = int(e["epic"][:2])
            lo = 1 + ((n - 1) // PART_SIZE) * PART_SIZE
            parts[part_name(lo, min(lo + PART_SIZE - 1, max_epic))].append(e)
        else:
            main_entries.append(e)

    counts: dict[str, int] = defaultdict(int)
    for e in entries:
        counts[e["type"]] += 1
    first, last = min(e["num"] for e in epics.values()), max_epic
    wiki = {
        "version": "2.0",
        "project": "Maktaba",
        "description": ("Self-hosted media intelligence platform: full Plex alternative + "
                        "every-word-searchable transcripts. See specs/architecture.md."),
        "generated_from": [
            f"specs/epics/** ({len(epics)} epics, {first:02d} to {last:02d}: READMEs, stories, plans)",
            "specs/architecture.md (DB tables)",
            f"specs/*.md ({len(reviews)} reviews and audits)",
            f"specs/diagrams/*.drawio ({len(diagrams)} diagrams)",
            "shared/api/openapi.yaml",
            "web/mockups/**/*.html",
            "docs/wiki/db/curated.json (hand-kept)",
        ],
        "counts": dict(sorted(counts.items())),
        "parts": list(parts),
        "entries": main_entries,
    }
    out: dict[Path, str] = {OUT_DB / "wiki.json": dump(wiki)}
    for name, ents in parts.items():
        out[OUT_DB / name] = dump({"version": "2.0", "part": name, "entries": ents})

    xref_cur = curated.get("cross_refs", {})
    xref = {
        "schema_version": 2,
        "generated_for": "Maktaba",
        "totals": {
            "epics": len(epics), "stories": len(stories), "plans": len(plans),
            "diagrams": len(diagrams), "mockups": len(mockups),
        },
        "epics": [
            {"id": f"{e['num']:02d}", "slug": en[3:], "title": e["name"],
             "description": (overlays.get(f"epic-{en}", {}).get("summary") or e["goal"]),
             "readme": e["readme"]}
            for en, e in epics.items()
        ],
        "stories": [
            {"id": (f"{int(s['epic'][:2]):02d}.{s['nn']:02d}" if s["nn"] is not None else s["id"]),
             "epic": int(s["epic"][:2]), "num": (f"{s['nn']:02d}" if s["nn"] is not None else s["slug"]),
             "slug": s["slug"], "title": s["title"], "story_file": s["story_file"],
             "plan_files": [s["plan_file"]] if s["plan_file"] else []}
            for s in (stories[k] for k in sorted(stories, key=nat_key))
        ],
        "diagrams": [{"key": d["name"], "path": d["file"]} for d in diagrams],
    }
    for k in ("files", "build_order", "critical_path", "parallel_tracks", "reviews",
              "grpc_rpcs", "notify_channels"):
        if k in xref_cur:
            xref[k] = xref_cur[k]
    out[OUT_DB / "wiki-cross-refs.json"] = dump(scrub(xref))
    out[SCHEMA_OUT] = json.dumps(SCHEMA, indent=2) + "\n"

    # ---- 9. the Arabic database ------------------------------------------------------------
    # the same files in Arabic, from the translation store (wiki_ar.py)
    openapi_ar = json.loads(read(OPENAPI_AR) or "{}").get("strings", {})
    ar = wiki_ar.translate(entries, wiki_ar.load_store(AR_STORE), openapi_ar, scrub_text)
    ar_by_id = {e["id"]: e for e in ar.entries}
    ar_wiki = {**wiki, "lang": "ar", "description": wiki_ar.DESCRIPTION_AR,
               "entries": [ar_by_id[e["id"]] for e in main_entries]}
    out[OUT_DB_AR / "wiki.json"] = dump(ar_wiki)
    for name, ents in parts.items():
        out[OUT_DB_AR / name] = dump({"version": "2.0", "part": name, "lang": "ar",
                                      "entries": [ar_by_id[e["id"]] for e in ents]})
    out[OUT_DB_AR / "wiki-cross-refs.json"] = dump(scrub(wiki_ar.cross_refs(xref, ar_by_id)))
    return Built(out, ar)


def build() -> dict[Path, str]:
    """The files the generator writes: path -> text."""
    return build_all().files


SCHEMA = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$id": "https://github.com/Hamza-Labs-Core/Maktaba/blob/main/docs/wiki/db/wiki-schema.json",
    "title": "Maktaba Wiki Database",
    "description": ("Machine-readable wiki of the Maktaba project: epics, stories, plans, diagrams, "
                    "reviews, mockups, API endpoints, DB entities, phases. wiki.json holds the meta "
                    "and every entry but the stories and plans, which are in the files `parts` names "
                    "(same shape: an `entries` array)."),
    "type": "object",
    "required": ["version", "project", "entries"],
    "properties": {
        "version": {"type": "string"},
        "project": {"type": "string"},
        "description": {"type": "string"},
        "generated_from": {"type": "array", "items": {"type": "string"}},
        "counts": {"type": "object", "additionalProperties": {"type": "integer"},
                   "description": "Entries per type, across wiki.json and its parts."},
        "parts": {"type": "array", "items": {"type": "string"},
                  "description": "The part files (beside wiki.json) holding the stories and plans."},
        "lang": {"type": "string", "enum": ["ar"],
                 "description": "The Arabic database (db/ar/): its text in Arabic. Absent in English."},
        "entries": {"type": "array", "items": {"$ref": "#/$defs/entry"}},
    },
    "$defs": {
        "entry": {
            "type": "object",
            "required": ["id", "type", "title"],
            "properties": {
                "id": {"type": "string", "description": "Stable identifier; cross-references use this."},
                "type": {"type": "string", "enum": ["epic", "story", "plan", "diagram", "review", "mockup",
                                                    "endpoint", "entity", "feature", "phase"]},
                "title": {"type": "string"},
                "epic": {"type": ["string", "null"]},
                "phase": {"type": ["integer", "null"]},
                "engine": {"type": ["string", "null"]},
                "content": {"type": "string", "description": "Markdown body or short description."},
                "tags": {"type": "array", "items": {"type": "string"}},
                "related": {"type": "array", "items": {"type": "string"},
                            "description": "Other entry ids related to this one."},
                "files": {
                    "type": "object",
                    "additionalProperties": {"anyOf": [{"type": "string"},
                                                       {"type": "array", "items": {"type": "string"}},
                                                       {"type": "null"}]},
                    "description": "Map of role -> repo-relative file path(s).",
                },
                "api_endpoints": {"type": "array", "items": {"type": "string"},
                                  "description": "Endpoints in 'METHOD /path' form mentioned by this entry."},
                "db_tables": {"type": "array", "items": {"type": "string"},
                              "description": "DB tables mentioned by this entry."},
                "metadata": {"type": "object"},
                "i18n": {
                    "type": "object",
                    "description": ("In the Arabic database: the language, and for each field shown "
                                    "in Arabic the hash of the English it translates (the drift check)."),
                    "properties": {
                        "lang": {"type": "string", "enum": ["ar"]},
                        "source": {"type": "object", "additionalProperties": {"type": "string"}},
                    },
                },
            },
        }
    },
}


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    ap.add_argument("--check", action="store_true",
                    help="fail if the committed database is stale or an entry lacks its Arabic")
    ap.add_argument("--ar-todo", action="store_true",
                    help="print, as JSON, every text with no Arabic or whose English changed: "
                         "store file -> entry id -> field -> the hash to record and the English")
    ap.add_argument("--epic", type=int, help="with --ar-todo: only Epic N's store file")
    args = ap.parse_args(argv)
    built = build_all()
    out = built.files
    if args.ar_todo:
        todo = {k: v for k, v in sorted(built.todo.items())
                if args.epic is None or k == f"epic-{args.epic:02d}.json"}
        print(json.dumps({wiki_ar.store_path(k): v for k, v in todo.items()},
                         indent=2, ensure_ascii=False))
        return 0
    stale_parts = [p for d in (OUT_DB, OUT_DB_AR) for p in d.glob("wiki-epics-*.json") if p not in out]
    if args.check:
        bad = [rel(p) for p, text in out.items() if read(p) != text] + [rel(p) for p in stale_parts]
        if bad:
            print("wiki database is out of date with the specs:", file=sys.stderr)
            for b in bad:
                print(f"  {b}", file=sys.stderr)
            print("run: python3 docs/wiki/db/generate_wiki.py  (and commit the result)", file=sys.stderr)
        if built.findings:
            print_findings(built.findings)
        if bad or built.findings:
            return 1
        print(f"wiki database matches the specs ({len(out)} files), every entry in English and "
              f"Arabic ({built.translated} of {built.total})")
        return 0
    OUT_DB_AR.mkdir(parents=True, exist_ok=True)
    for p, text in out.items():
        if read(p) != text:
            p.write_text(text, encoding="utf-8")
            print(f"wrote {rel(p)}")
    for p in stale_parts:
        p.unlink()
        print(f"removed {rel(p)}")
    if built.findings:
        print_findings(built.findings)
        print("(the Arabic database shows those texts in English until they are translated)",
              file=sys.stderr)
    return 0


def print_findings(findings: list[str]) -> None:
    print(f"the Arabic wiki is missing or behind the English ({len(findings)}):", file=sys.stderr)
    for f in findings[:200]:
        print(f"  {f}", file=sys.stderr)
    if len(findings) > 200:
        print(f"  … and {len(findings) - 200} more", file=sys.stderr)
    print("every spec change needs its Arabic: edit the file and field named above "
          f"({wiki_ar.TODO_HINT})", file=sys.stderr)


if __name__ == "__main__":
    sys.exit(main())
