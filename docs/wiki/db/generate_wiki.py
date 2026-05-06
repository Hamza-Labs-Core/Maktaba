#!/usr/bin/env python3
"""Generate the Maktaba wiki database.

Reads:
  /tmp/maktaba-specs/specs/*           (specs from merge-origin-license)
  /tmp/maktaba-specs/shared/db/*       (migrations manifest)
  ./shared/api/openapi.yaml             (current branch)
  ./web/mockups/                        (current branch)

Writes:
  ./docs/wiki/INDEX.md
  ./docs/wiki/stories-map.md
  ./docs/wiki/features.md
  ./docs/wiki/entities.md
  ./docs/wiki/api-catalog.md
  ./docs/wiki/db/wiki.json
  ./docs/wiki/db/wiki-schema.json
"""

import json
import os
import re
import sys
from collections import defaultdict
from pathlib import Path

SPECS = Path("/tmp/maktaba-specs/specs")
SHARED = Path("/tmp/maktaba-specs/shared")
REPO = Path(".")
OUT = REPO / "docs" / "wiki"
DB = OUT / "db"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

EPIC_NAME_FROM_DIR = {
    "01-scanner": "Scanner",
    "02-audio-extraction": "Audio Extraction",
    "03-transcription": "Transcription",
    "04-subtitles": "Subtitles",
    "05-search-indexing": "Search & Indexing",
    "06-job-queue": "Job Queue",
    "07-api-server": "API Server",
    "08-streaming": "Streaming",
    "09-library-management": "Library Management",
    "10-auth-security": "Auth & Security",
    "11-web-ui": "Web UI",
    "12-mobile": "Mobile",
    "13-desktop": "Desktop",
    "14-tv-apps": "TV Apps",
    "15-discovery": "Discovery",
    "16-subscriptions": "Subscriptions",
    "17-ux-design-system": "UX & Design System",
    "18-performance": "Performance",
    "19-scalability": "Scalability",
    "20-testing": "Testing",
    "21-observability": "Observability",
    "22-devops": "DevOps",
    "23-security": "Security Hardening",
    "24-data-integrity": "Data Integrity",
}

EPIC_PHASE = {
    # rough phase grouping based on epic ranges; not in source files
    "01-scanner": 1, "02-audio-extraction": 1, "03-transcription": 1,
    "04-subtitles": 1, "05-search-indexing": 1, "06-job-queue": 1,
    "07-api-server": 2, "08-streaming": 2,
    "09-library-management": 2, "10-auth-security": 2,
    "11-web-ui": 3, "12-mobile": 3, "13-desktop": 3, "14-tv-apps": 3,
    "15-discovery": 3, "16-subscriptions": 3, "17-ux-design-system": 3,
    "18-performance": 4, "19-scalability": 4, "20-testing": 4,
    "21-observability": 4, "22-devops": 4, "23-security": 4,
    "24-data-integrity": 4,
}

EPIC_ENGINE = {
    "01-scanner": "Pipeline (Python)",
    "02-audio-extraction": "Pipeline (Python)",
    "03-transcription": "Pipeline (Python)",
    "04-subtitles": "Pipeline (Python)",
    "05-search-indexing": "Pipeline (Python) + API (Go)",
    "06-job-queue": "Pipeline + API (cross-cutting)",
    "07-api-server": "API (Go)",
    "08-streaming": "Streaming (Go)",
    "09-library-management": "API (Go)",
    "10-auth-security": "API (Go)",
    "11-web-ui": "Web (React/TS)",
    "12-mobile": "Mobile (Capacitor + plugins)",
    "13-desktop": "Desktop (Tauri)",
    "14-tv-apps": "TV (Swift / Kotlin)",
    "15-discovery": "API (Go) + Web",
    "16-subscriptions": "API (Go) + Pipeline",
    "17-ux-design-system": "Web (React/TS)",
    "18-performance": "Cross-cutting",
    "19-scalability": "Cross-cutting",
    "20-testing": "Cross-cutting",
    "21-observability": "Cross-cutting",
    "22-devops": "Cross-cutting",
    "23-security": "API + Streaming",
    "24-data-integrity": "Cross-cutting",
}


def read(p: Path) -> str:
    try:
        return p.read_text(encoding="utf-8")
    except Exception:
        return ""


def parse_story_id(filename: str):
    """story-EE-NN-slug.md  →  (EE, NN, slug, 'EE.NN')"""
    m = re.match(r"^story-(\d{2})-(\d{2})-(.+)\.md$", filename)
    if not m:
        return None
    return m.group(1), m.group(2), m.group(3), f"{int(m.group(1))}.{int(m.group(2))}"


def parse_plan_id(filename: str):
    m = re.match(r"^plan-(\d{2})-(\d{2})-(.+)\.md$", filename)
    if not m:
        return None
    return m.group(1), m.group(2), m.group(3)


def first_heading_title(text: str) -> str:
    for line in text.splitlines():
        if line.startswith("# "):
            return line[2:].strip()
    return ""


def extract_story_title(text: str) -> str:
    """First H1 like '# Story 1.1 — Bootstrap a library'."""
    h = first_heading_title(text)
    # Strip "Story X.Y —" / "Story X.Y -"
    h = re.sub(r"^Story\s+\d+\.\d+\s*[—–-]\s*", "", h)
    return h


def extract_plan_title(text: str) -> str:
    h = first_heading_title(text)
    h = re.sub(r"^Plan\s+\d+\.\d+\s*[—–-]\s*", "", h)
    return h


def slug_to_title(slug: str) -> str:
    return slug.replace("-", " ").title()


# ---------------------------------------------------------------------------
# 1. Inventory stories / plans / READMEs
# ---------------------------------------------------------------------------

epics = {}   # epic_dir -> dict
stories = {}  # 'EE.NN' -> dict
plans = {}    # plan_id -> dict

for epic_dir in sorted((SPECS / "epics").iterdir()):
    if not epic_dir.is_dir():
        continue
    en = epic_dir.name
    readme = read(epic_dir / "README.md")
    epic_goal = ""
    for line in readme.splitlines():
        s = line.strip()
        if s.startswith("**Goal.**"):
            epic_goal = s.replace("**Goal.**", "").strip()
            break
    if not epic_goal:
        # fallback to second line of README
        readme_lines = [l for l in readme.splitlines() if l.strip()]
        if len(readme_lines) > 1:
            epic_goal = readme_lines[1].strip()

    epic_stories = []
    epic_plans = []
    for f in sorted(epic_dir.iterdir()):
        if f.name.startswith("story-"):
            sid = parse_story_id(f.name)
            if not sid:
                continue
            ee, nn, slug, dotted = sid
            text = read(f)
            title = extract_story_title(text) or slug_to_title(slug)
            stories[dotted] = {
                "id": dotted,
                "ee": ee, "nn": nn, "slug": slug,
                "title": title,
                "epic": en,
                "epic_name": EPIC_NAME_FROM_DIR.get(en, en),
                "story_file": f"specs/epics/{en}/{f.name}",
                "plan_file": None,
                "raw": text,
            }
            epic_stories.append(dotted)
        elif f.name.startswith("plan-"):
            pid = parse_plan_id(f.name)
            if not pid:
                continue
            ee, nn, slug = pid
            text = read(f)
            title = extract_plan_title(text) or slug_to_title(slug)
            plan_key = f"{ee}.{nn}-{slug}"
            plans[plan_key] = {
                "id": plan_key,
                "ee": ee, "nn": nn, "slug": slug,
                "title": title,
                "epic": en,
                "plan_file": f"specs/epics/{en}/{f.name}",
                "raw": text,
            }
            epic_plans.append(plan_key)
    epics[en] = {
        "id": en,
        "name": EPIC_NAME_FROM_DIR.get(en, en),
        "phase": EPIC_PHASE.get(en),
        "engine": EPIC_ENGINE.get(en, "—"),
        "goal": epic_goal,
        "readme": f"specs/epics/{en}/README.md",
        "stories": epic_stories,
        "plans": epic_plans,
    }

# Match plans → stories (same EE.NN often pairs with story EE.NN)
for plan in plans.values():
    dotted = f"{int(plan['ee'])}.{int(plan['nn'])}"
    if dotted in stories:
        # only set if matching slug or first match
        if not stories[dotted]["plan_file"] or stories[dotted]["slug"] == plan["slug"]:
            stories[dotted]["plan_file"] = plan["plan_file"]
            stories[dotted]["plan_key"] = plan["id"]

# ---------------------------------------------------------------------------
# 2. Diagrams
# ---------------------------------------------------------------------------

diagrams = []
for d in sorted((SPECS / "diagrams").iterdir()):
    if d.suffix == ".drawio":
        name = d.stem
        diagrams.append({
            "id": f"diagram-{name}",
            "name": name,
            "title": slug_to_title(name),
            "file": f"specs/diagrams/{d.name}",
        })

# ---------------------------------------------------------------------------
# 3. Reviews
# ---------------------------------------------------------------------------

reviews = []
for r in sorted(SPECS.glob("REVIEW*.md")):
    reviews.append({
        "id": f"review-{r.stem.lower()}",
        "name": r.stem,
        "file": f"specs/{r.name}",
    })
for r in sorted(SPECS.glob("PLAN_REVIEW*.md")):
    reviews.append({
        "id": f"review-{r.stem.lower()}",
        "name": r.stem,
        "file": f"specs/{r.name}",
    })

# ---------------------------------------------------------------------------
# 4. Mockups (current repo)
# ---------------------------------------------------------------------------

mockups = []
mockup_root = REPO / "web" / "mockups"
for m in sorted(mockup_root.rglob("*.html")):
    rel = m.relative_to(REPO).as_posix()
    name = m.stem
    title = slug_to_title(name)
    # try to map to a story by 'mockup-EE-NN-slug.html'
    story_id = None
    sm = re.match(r"^mockup-(\d{2})-(\d{2})-", name)
    if sm:
        story_id = f"{int(sm.group(1))}.{int(sm.group(2))}"
    mockups.append({
        "id": f"mockup-{name}",
        "name": name,
        "title": title,
        "file": rel,
        "story": story_id,
        "category": m.parent.name if m.parent.name != "mockups" else "shared",
    })

# Attach mockups to stories where directly mapped
for mk in mockups:
    if mk["story"] and mk["story"] in stories:
        stories[mk["story"]].setdefault("mockup_files", []).append(mk["file"])

# ---------------------------------------------------------------------------
# 5. OpenAPI endpoints
# ---------------------------------------------------------------------------

oapi_path = REPO / "shared" / "api" / "openapi.yaml"
oapi_text = read(oapi_path)
endpoints = []
# Light hand-rolled YAML walker (paths section only)
in_paths = False
current_path = None
for raw_line in oapi_text.splitlines():
    if raw_line.startswith("paths:"):
        in_paths = True
        continue
    if not in_paths:
        continue
    # End of paths section: another top-level key with no leading space
    if raw_line and not raw_line.startswith(" ") and not raw_line.startswith("#"):
        in_paths = False
        continue
    m_path = re.match(r"^  (/[A-Za-z0-9._/{}-]+):\s*$", raw_line)
    if m_path:
        current_path = m_path.group(1)
        continue
    m_method = re.match(r"^    (get|post|put|patch|delete|options|head):\s*$", raw_line)
    if m_method and current_path:
        method = m_method.group(1).upper()
        endpoints.append({
            "id": f"ep-{method.lower()}-{re.sub(r'[^A-Za-z0-9]+', '-', current_path).strip('-')}",
            "method": method, "path": current_path,
            "summary": "", "operationId": "", "tag": "",
        })
# Pull operationId / summary / tags by re-parsing per endpoint (simple line scan)
lines = oapi_text.splitlines()
for ep in endpoints:
    # find the line of the path then the line of the method then look ahead
    for i, line in enumerate(lines):
        if line == f"  {ep['path']}:":
            # search for the method within ~80 lines
            for j in range(i + 1, min(i + 800, len(lines))):
                lj = lines[j]
                if re.match(r"^    [a-z]+:\s*$", lj):
                    if lj.strip().rstrip(":").lower() == ep["method"].lower():
                        # next ~30 lines: scan for summary/operationId/tags
                        for k in range(j + 1, min(j + 60, len(lines))):
                            lk = lines[k]
                            if re.match(r"^    [a-z]+:\s*$", lk):  # next method
                                break
                            if lk.startswith("  /"):  # next path
                                break
                            ms = re.match(r"^      summary:\s*(.+)$", lk)
                            if ms:
                                ep["summary"] = ms.group(1).strip().strip('"\'')
                            mo = re.match(r"^      operationId:\s*(.+)$", lk)
                            if mo:
                                ep["operationId"] = mo.group(1).strip().strip('"\'')
                            if lk.strip().startswith("tags:"):
                                # next line(s) starting with '- '
                                for kk in range(k + 1, min(k + 8, len(lines))):
                                    lkk = lines[kk]
                                    mt = re.match(r"^      - (.+)$", lkk)
                                    if mt:
                                        ep["tag"] = mt.group(1).strip().strip('"\'')
                                        break
                                    if not lkk.startswith("        "):
                                        break
                        break
            break

# ---------------------------------------------------------------------------
# 6. DB entities (parse architecture §8 + plan-introduced extensions)
# ---------------------------------------------------------------------------

arch_text = read(SPECS / "architecture.md")
manifest_text = read(SHARED / "db" / "migrations" / "MANIFEST.md")

# Find every CREATE TABLE in §8.* of architecture
entities = {}
for m in re.finditer(r"CREATE\s+TABLE(?:\s+IF\s+NOT\s+EXISTS)?\s+(\w+)\s*\(", arch_text):
    name = m.group(1)
    if name in ("AS", "(", ""):
        continue
    if name not in entities:
        entities[name] = {
            "id": f"entity-{name}",
            "name": name,
            "source": "architecture.md §8",
            "migration_slot": None,
            "owning_plan": None,
        }

# Plan-introduced tables from §8.7 table
m87 = re.search(r"\*\*Plan-introduced tables\.\*\*[\s\S]*?\|-+\|[\s\S]*?(?=\n\n)", arch_text)
if m87:
    for row in re.findall(r"^\|\s*`?(\w+)`?\s*\|\s*([^|]+)\|\s*([0-9]{4})\s*\|", m87.group(0), re.MULTILINE):
        name, plan_cell, slot = row
        if name not in entities:
            entities[name] = {"id": f"entity-{name}", "name": name, "source": "architecture.md §8.7", "migration_slot": slot, "owning_plan": plan_cell.strip().strip('`')}
        else:
            entities[name]["migration_slot"] = slot
            entities[name]["owning_plan"] = plan_cell.strip().strip('`')

# Fill migration slot owners from MANIFEST (slot -> plan, summary)
manifest_rows = []
for row in re.finditer(r"^\|\s*`(\d{4})`\s*\|\s*`([^`]+)`\s*\|\s*\[([^\]]+)\]\(([^)]+)\)\s*\|\s*([^|]*)\|\s*(.+?)\s*\|$", manifest_text, re.MULTILINE):
    slot, fname, plan, plan_path, deps, summary = row.groups()
    manifest_rows.append({
        "slot": slot, "filename": fname, "plan": plan,
        "plan_path": plan_path, "depends_on": deps.strip(),
        "summary": summary.strip(),
    })

# Try to attach migration slots to entities by parsing summary
for r in manifest_rows:
    s = r["summary"].lower()
    for ent in entities:
        if f"`{ent.lower()}`" in s or f" {ent.lower()} " in s:
            entities[ent].setdefault("migrations", []).append(r["slot"])

# Build entity → stories map (heuristic via raw story text mentioning the table name in backticks)
table_to_stories = defaultdict(set)
table_to_plans = defaultdict(set)
for sid, story in stories.items():
    txt = story["raw"]
    for ent in entities:
        if re.search(r"`%s`" % re.escape(ent), txt):
            table_to_stories[ent].add(sid)
for pid, plan in plans.items():
    txt = plan["raw"]
    for ent in entities:
        if re.search(r"`%s`" % re.escape(ent), txt):
            table_to_plans[ent].add(pid)

# Build endpoint → story map (heuristic via path mention in story text)
endpoint_to_stories = defaultdict(set)
for ep in endpoints:
    pat = ep["path"]
    # search for the literal path in story bodies
    needle = pat
    for sid, story in stories.items():
        if needle in story["raw"]:
            endpoint_to_stories[(ep["method"], ep["path"])].add(sid)

# Story → endpoints
story_to_endpoints = defaultdict(list)
for (method, path), sids in endpoint_to_stories.items():
    for sid in sids:
        story_to_endpoints[sid].append(f"{method} {path}")

# Story → tables
story_to_tables = defaultdict(list)
for tbl, sids in table_to_stories.items():
    for sid in sids:
        story_to_tables[sid].append(tbl)

# ---------------------------------------------------------------------------
# 7. Write INDEX.md
# ---------------------------------------------------------------------------

OUT.mkdir(parents=True, exist_ok=True)
DB.mkdir(parents=True, exist_ok=True)


def w(path: Path, content: str):
    path.write_text(content, encoding="utf-8")
    print(f"wrote {path}")


index_lines = [
    "# Maktaba Wiki — Master Index",
    "",
    "> Comprehensive index of every artifact in the Maktaba project: 24 epics,",
    f"> {len(stories)} stories, {len(plans)} plans, {len(diagrams)} diagrams,",
    f"> {len(mockups)} mockups, {len(endpoints)} API endpoints, {len(entities)} DB entities.",
    "",
    "## Quick navigation",
    "",
    "| Catalog | File | Entries |",
    "|---|---|---|",
    f"| Stories | [stories-map.md](stories-map.md) | {len(stories)} |",
    f"| Features | [features.md](features.md) | per epic |",
    f"| DB entities | [entities.md](entities.md) | {len(entities)} |",
    f"| API endpoints | [api-catalog.md](api-catalog.md) | {len(endpoints)} |",
    f"| Machine-readable DB | [db/wiki.json](db/wiki.json) | {len(stories)+len(plans)+len(epics)+len(diagrams)+len(mockups)+len(endpoints)+len(entities)+len(reviews)} entries |",
    f"| JSON schema | [db/wiki-schema.json](db/wiki-schema.json) | — |",
    "",
    "## Epic table",
    "",
    "| # | Epic | Phase | Engine | Stories | Plans | Mockups | Goal |",
    "|---|---|---|---|---|---|---|---|",
]

# Per-epic mockup count: count mockups whose story id starts with that epic, plus admin/desktop/etc.
admin_mockups = [m for m in mockups if m["category"] == "admin"]
desktop_mockups = [m for m in mockups if m["category"] == "desktop"]
mobile_mockups = [m for m in mockups if m["category"] == "mobile"]
tv_mockups = [m for m in mockups if m["category"] == "tv"]
themelib_mockups = [m for m in mockups if m["category"] == "theme-library"]

# rough per-epic mockup attribution
epic_to_mockups = defaultdict(list)
for mk in mockups:
    if mk["story"]:
        ee = mk["story"].split(".")[0].zfill(2)
        for ed in epics:
            if ed.startswith(ee + "-"):
                epic_to_mockups[ed].append(mk)
                break
    elif mk["category"] == "admin":
        # admin maps mostly to 09-library-management, 10-auth-security, 21-observability
        epic_to_mockups["09-library-management"].append(mk)
    elif mk["category"] == "desktop":
        epic_to_mockups["13-desktop"].append(mk)
    elif mk["category"] == "mobile":
        epic_to_mockups["12-mobile"].append(mk)
    elif mk["category"] == "tv":
        epic_to_mockups["14-tv-apps"].append(mk)
    elif mk["category"] == "theme-library":
        epic_to_mockups["17-ux-design-system"].append(mk)

for en, e in epics.items():
    num = en.split("-")[0]
    goal_short = (e["goal"][:90] + "…") if len(e["goal"]) > 90 else e["goal"]
    n_mockups = len(epic_to_mockups.get(en, []))
    index_lines.append(
        f"| {num} | [{e['name']}](../../{e['readme']}) | {e['phase'] or '—'} | {e['engine']} | {len(e['stories'])} | {len(e['plans'])} | {n_mockups} | {goal_short} |"
    )

index_lines += [
    "",
    "## Diagrams",
    "",
    "| Diagram | File |",
    "|---|---|",
]
for d in diagrams:
    index_lines.append(f"| {d['title']} | [{d['file']}](../../{d['file']}) |")

index_lines += [
    "",
    "## Reviews",
    "",
    "| Review | File |",
    "|---|---|",
]
for r in reviews:
    index_lines.append(f"| {r['name']} | [{r['file']}](../../{r['file']}) |")

index_lines += [
    "",
    "## Cross-platform mockups",
    "",
    "| Surface | Count | Folder |",
    "|---|---|---|",
    f"| Admin (web) | {len(admin_mockups)} | [web/mockups/admin/](../../web/mockups/admin/) |",
    f"| Mobile | {len(mobile_mockups)} | [web/mockups/mobile/](../../web/mockups/mobile/) |",
    f"| Desktop | {len(desktop_mockups)} | [web/mockups/desktop/](../../web/mockups/desktop/) |",
    f"| TV | {len(tv_mockups)} | [web/mockups/tv/](../../web/mockups/tv/) |",
    f"| Theme library | {len(themelib_mockups)} | [web/mockups/theme-library/](../../web/mockups/theme-library/) |",
    "",
    "## Reference docs",
    "",
    "- [specs/architecture.md](../../specs/architecture.md) — full system architecture (12 sections + 3 appendices)",
    "- [shared/api/openapi.yaml](../../shared/api/openapi.yaml) — OpenAPI 3.1 spec",
    "- [shared/db/migrations/MANIFEST.md](../../shared/db/migrations/MANIFEST.md) — migration slot manifest",
    "",
    "*Generated by `docs/wiki/db/wiki.json` · cross-references derive from `git`-tracked files in this branch and the spec branch (`merge-origin-license`).*",
]

w(OUT / "INDEX.md", "\n".join(index_lines) + "\n")

# ---------------------------------------------------------------------------
# 8. stories-map.md
# ---------------------------------------------------------------------------

sm_lines = [
    "# Stories Map",
    "",
    f"All {len(stories)} stories across 24 epics. Cross-referenced against plans, mockups,",
    "API endpoints (from `openapi.yaml`), DB tables (from architecture §8), and diagrams.",
    "",
    "Linear issue IDs are not embedded in story files — column shown for completeness.",
    "",
    "| ID | Title | Epic | Phase | Plan | Mockup | API | Tables |",
    "|----|-------|------|-------|------|--------|-----|--------|",
]
for sid in sorted(stories.keys(), key=lambda s: tuple(int(x) for x in s.split("."))):
    st = stories[sid]
    plan_cell = f"[plan]({{}})".format("../../" + st["plan_file"]) if st.get("plan_file") else "—"
    mk_cells = st.get("mockup_files", [])
    mk_cell = ", ".join(f"[m{i+1}](../../{m})" for i, m in enumerate(mk_cells)) if mk_cells else "—"
    eps = story_to_endpoints.get(sid, [])
    api_cell = ", ".join(f"`{e}`" for e in eps[:3]) + (f" + {len(eps) - 3}" if len(eps) > 3 else "") if eps else "—"
    tbls = story_to_tables.get(sid, [])
    tbl_cell = ", ".join(f"`{t}`" for t in tbls[:3]) + (f" + {len(tbls) - 3}" if len(tbls) > 3 else "") if tbls else "—"
    sm_lines.append(
        f"| **[{sid}](../../{st['story_file']})** | {st['title']} | {st['epic_name']} "
        f"| {EPIC_PHASE.get(st['epic'], '—')} | {plan_cell} | {mk_cell} | {api_cell} | {tbl_cell} |"
    )

w(OUT / "stories-map.md", "\n".join(sm_lines) + "\n")

# ---------------------------------------------------------------------------
# 9. features.md
# ---------------------------------------------------------------------------

f_lines = [
    "# Feature catalog",
    "",
    "Every feature in Maktaba, organized by epic. Each entry lists the stories that",
    "implement it, the plans that detail it, the mockups that show it, and the API",
    "endpoints that serve it.",
    "",
]
for en, e in epics.items():
    f_lines += [
        f"## Epic {en.split('-')[0]} — {e['name']}",
        "",
        f"**Engine:** {e['engine']}  ·  **Phase:** {e['phase']}",
        "",
        f"**Goal.** {e['goal']}",
        "",
        f"📖 [Epic README](../../{e['readme']})",
        "",
        "### Features",
        "",
    ]
    for sid in e["stories"]:
        st = stories[sid]
        eps = story_to_endpoints.get(sid, [])
        tbls = story_to_tables.get(sid, [])
        mks = st.get("mockup_files", [])
        f_lines.append(f"#### {sid} — {st['title']}")
        f_lines.append("")
        f_lines.append(f"- 📄 Story: [{st['story_file']}](../../{st['story_file']})")
        if st.get("plan_file"):
            f_lines.append(f"- 🛠 Plan: [{st['plan_file']}](../../{st['plan_file']})")
        if mks:
            f_lines.append("- 🖼 Mockups: " + ", ".join(f"[{Path(m).name}](../../{m})" for m in mks))
        if eps:
            f_lines.append("- 🔌 API: " + ", ".join(f"`{e}`" for e in eps[:5]) + (f" *(+{len(eps) - 5})*" if len(eps) > 5 else ""))
        if tbls:
            f_lines.append("- 🗄 Tables: " + ", ".join(f"`{t}`" for t in tbls[:6]) + (f" *(+{len(tbls) - 6})*" if len(tbls) > 6 else ""))
        f_lines.append("")
    f_lines.append("")

w(OUT / "features.md", "\n".join(f_lines))

# ---------------------------------------------------------------------------
# 10. entities.md
# ---------------------------------------------------------------------------

e_lines = [
    "# DB Entity catalog",
    "",
    "Every database table in Maktaba — base schema (architecture §8.1–§8.6) and plan-",
    "introduced extensions (§8.7). Owning migration is from",
    "[`shared/db/migrations/MANIFEST.md`](../../shared/db/migrations/MANIFEST.md);",
    "stories and plans are derived by scanning their text for backticked table names.",
    "",
    "| Table | Source | Migration | Owning plan | Stories | Plans (count) |",
    "|-------|--------|-----------|-------------|---------|---------------|",
]
for name in sorted(entities):
    ent = entities[name]
    sids = sorted(table_to_stories.get(name, set()), key=lambda s: tuple(int(x) for x in s.split(".")))[:6]
    s_cell = ", ".join(f"[{s}](stories-map.md#{s.replace('.', '')})" for s in sids) or "—"
    if len(table_to_stories.get(name, set())) > 6:
        s_cell += f" *(+{len(table_to_stories[name]) - 6})*"
    p_count = len(table_to_plans.get(name, set()))
    e_lines.append(
        f"| `{name}` | {ent['source']} | "
        f"{ent.get('migration_slot') or '—'} | "
        f"{ent.get('owning_plan') or '—'} | "
        f"{s_cell} | {p_count} |"
    )

e_lines += [
    "",
    "## Migration manifest (full)",
    "",
    "| Slot | File | Plan | Depends on | Summary |",
    "|------|------|------|-----------|---------|",
]
for r in manifest_rows:
    # plan_path is relative to MANIFEST.md (../../specs/epics/...).
    # docs/wiki/entities.md has the same depth, so the prefix matches.
    e_lines.append(
        f"| `{r['slot']}` | `{r['filename']}` | [{r['plan']}]({r['plan_path']}) | "
        f"{r['depends_on']} | {r['summary']} |"
    )

w(OUT / "entities.md", "\n".join(e_lines) + "\n")

# ---------------------------------------------------------------------------
# 11. api-catalog.md
# ---------------------------------------------------------------------------

a_lines = [
    "# API catalog",
    "",
    f"All {len(endpoints)} REST/WebSocket endpoints in",
    "[`shared/api/openapi.yaml`](../../shared/api/openapi.yaml). Each row links the",
    "endpoint to the stories that mention it and to a candidate mockup that shows",
    "the UI for that surface.",
    "",
    "| Method | Path | Tag | Summary | Owning stories |",
    "|--------|------|-----|---------|----------------|",
]
for ep in sorted(endpoints, key=lambda e: (e["tag"] or "zzz", e["path"], e["method"])):
    sids = sorted(endpoint_to_stories.get((ep["method"], ep["path"]), set()),
                  key=lambda s: tuple(int(x) for x in s.split(".")))
    s_cell = ", ".join(f"[{s}](stories-map.md)" for s in sids[:5]) + (f" *(+{len(sids) - 5})*" if len(sids) > 5 else "") if sids else "—"
    summary = ep["summary"].replace("|", "\\|")[:80]
    a_lines.append(f"| `{ep['method']}` | `{ep['path']}` | {ep['tag'] or '—'} | {summary} | {s_cell} |")

w(OUT / "api-catalog.md", "\n".join(a_lines) + "\n")

# ---------------------------------------------------------------------------
# 12. wiki.json + wiki-schema.json
# ---------------------------------------------------------------------------

# Build entries
entries = []

# Epics
for en, e in epics.items():
    related = []
    related.extend([f"story-{sid.replace('.', '-').zfill(0)}" for sid in e["stories"]])  # noop placeholder; we use 'story-EE.NN'
    related = [f"story-{sid}" for sid in e["stories"]] + [f"plan-{p}" for p in e["plans"]]
    entries.append({
        "id": f"epic-{en}",
        "type": "epic",
        "title": e["name"],
        "epic": en,
        "phase": e["phase"],
        "engine": e["engine"],
        "content": e["goal"],
        "tags": ["epic", en, f"phase-{e['phase']}"],
        "related": related,
        "files": {"readme": e["readme"]},
        "metadata": {
            "story_count": len(e["stories"]),
            "plan_count": len(e["plans"]),
            "mockup_count": len(epic_to_mockups.get(en, [])),
        },
    })

# Stories
for sid, st in stories.items():
    eps = story_to_endpoints.get(sid, [])
    tbls = story_to_tables.get(sid, [])
    mks = st.get("mockup_files", [])
    related = [f"epic-{st['epic']}"]
    if st.get("plan_key"):
        related.append(f"plan-{st['plan_key']}")
    for t in tbls:
        related.append(f"entity-{t}")
    for e in eps:
        method, path = e.split(" ", 1)
        related.append(f"ep-{method.lower()}-{re.sub(r'[^A-Za-z0-9]+', '-', path).strip('-')}")
    for m in mks:
        related.append(f"mockup-{Path(m).stem}")
    entries.append({
        "id": f"story-{sid}",
        "type": "story",
        "title": st["title"],
        "epic": st["epic"],
        "phase": EPIC_PHASE.get(st["epic"]),
        "content": st["raw"][:600],
        "tags": ["story", st["epic"], f"phase-{EPIC_PHASE.get(st['epic'])}"],
        "related": related,
        "files": {
            "story": st["story_file"],
            "plan": st.get("plan_file"),
            "mockups": mks,
        },
        "linear": None,
        "api_endpoints": eps,
        "db_tables": tbls,
        "metadata": {"slug": st["slug"]},
    })

# Plans
for pid, plan in plans.items():
    related = [f"epic-{plan['epic']}"]
    # match story by EE.NN
    dotted = f"{int(plan['ee'])}.{int(plan['nn'])}"
    if dotted in stories:
        related.append(f"story-{dotted}")
    entries.append({
        "id": f"plan-{pid}",
        "type": "plan",
        "title": plan["title"],
        "epic": plan["epic"],
        "phase": EPIC_PHASE.get(plan["epic"]),
        "content": plan["raw"][:600],
        "tags": ["plan", plan["epic"]],
        "related": related,
        "files": {"plan": plan["plan_file"]},
        "metadata": {"slug": plan["slug"]},
    })

# Diagrams
for d in diagrams:
    entries.append({
        "id": d["id"],
        "type": "diagram",
        "title": d["title"],
        "content": f"Architecture diagram: {d['title']}.",
        "tags": ["diagram"],
        "related": [],
        "files": {"diagram": d["file"]},
    })

# Reviews
for r in reviews:
    entries.append({
        "id": r["id"],
        "type": "review",
        "title": r["name"],
        "content": f"Plan-review document: {r['name']}.",
        "tags": ["review"],
        "related": [],
        "files": {"review": r["file"]},
    })

# Mockups
for mk in mockups:
    related = []
    if mk["story"] and mk["story"] in stories:
        related.append(f"story-{mk['story']}")
    entries.append({
        "id": mk["id"],
        "type": "mockup",
        "title": mk["title"],
        "content": f"HTML mockup ({mk['category']}): {mk['title']}.",
        "tags": ["mockup", mk["category"]],
        "related": related,
        "files": {"mockup": mk["file"]},
        "metadata": {"category": mk["category"]},
    })

# API endpoints
for ep in endpoints:
    sids = sorted(endpoint_to_stories.get((ep["method"], ep["path"]), set()),
                  key=lambda s: tuple(int(x) for x in s.split(".")))
    related = [f"story-{s}" for s in sids]
    entries.append({
        "id": ep["id"],
        "type": "endpoint",
        "title": f"{ep['method']} {ep['path']}",
        "content": ep["summary"] or f"{ep['method']} {ep['path']}",
        "tags": ["api", ep["tag"] or "untagged", ep["method"].lower()],
        "related": related,
        "files": {"openapi": "shared/api/openapi.yaml"},
        "metadata": {
            "method": ep["method"],
            "path": ep["path"],
            "tag": ep["tag"],
            "operationId": ep["operationId"],
        },
    })

# Entities
for name, ent in entities.items():
    sids = sorted(table_to_stories.get(name, set()),
                  key=lambda s: tuple(int(x) for x in s.split(".")))
    related = [f"story-{s}" for s in sids]
    entries.append({
        "id": ent["id"],
        "type": "entity",
        "title": name,
        "content": f"Database table `{name}`. Source: {ent['source']}.",
        "tags": ["entity", "database"],
        "related": related,
        "files": {
            "schema": "specs/architecture.md#8-database-schema",
            "manifest": "shared/db/migrations/MANIFEST.md",
        },
        "metadata": {
            "source": ent["source"],
            "migration_slot": ent.get("migration_slot"),
            "owning_plan": ent.get("owning_plan"),
        },
    })

# Build counts
type_counts = defaultdict(int)
for e in entries:
    type_counts[e["type"]] += 1

wiki = {
    "version": "1.0",
    "project": "Maktaba",
    "description": "Self-hosted media intelligence platform: full Plex alternative + every-word-searchable transcripts. See specs/architecture.md.",
    "generated_from": [
        "specs/architecture.md (merge-origin-license)",
        "specs/epics/**/*.md (24 epics)",
        "specs/diagrams/*.drawio (14 diagrams)",
        "specs/REVIEW*.md, specs/PLAN_REVIEW*.md (5 reviews)",
        "shared/db/migrations/MANIFEST.md",
        "shared/api/openapi.yaml",
        "web/mockups/**/*.html",
    ],
    "counts": dict(type_counts),
    "entries": entries,
}

with open(DB / "wiki.json", "w", encoding="utf-8") as fh:
    json.dump(wiki, fh, indent=2, ensure_ascii=False)
print(f"wrote {DB / 'wiki.json'}  ({len(entries)} entries)")

# Schema
schema = {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$id": "https://maktaba.example/wiki-schema.json",
    "title": "Maktaba Wiki Database",
    "description": "Machine-readable wiki of the Maktaba project: epics, stories, plans, diagrams, reviews, mockups, API endpoints, DB entities.",
    "type": "object",
    "required": ["version", "project", "entries"],
    "properties": {
        "version": {"type": "string"},
        "project": {"type": "string"},
        "description": {"type": "string"},
        "generated_from": {"type": "array", "items": {"type": "string"}},
        "counts": {"type": "object", "additionalProperties": {"type": "integer"}},
        "entries": {
            "type": "array",
            "items": {"$ref": "#/$defs/entry"},
        },
    },
    "$defs": {
        "entry": {
            "type": "object",
            "required": ["id", "type", "title"],
            "properties": {
                "id": {"type": "string", "description": "Stable identifier; cross-references use this."},
                "type": {
                    "type": "string",
                    "enum": ["epic", "story", "plan", "diagram", "review", "mockup", "endpoint", "entity", "feature"],
                },
                "title": {"type": "string"},
                "epic": {"type": ["string", "null"]},
                "phase": {"type": ["integer", "null"]},
                "engine": {"type": ["string", "null"]},
                "content": {"type": "string", "description": "Markdown body or short description."},
                "tags": {"type": "array", "items": {"type": "string"}},
                "related": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Other entry ids related to this one.",
                },
                "files": {
                    "type": "object",
                    "additionalProperties": {
                        "anyOf": [
                            {"type": "string"},
                            {"type": "array", "items": {"type": "string"}},
                            {"type": "null"},
                        ]
                    },
                    "description": "Map of role -> repo-relative file path(s).",
                },
                "linear": {"type": ["string", "null"], "description": "Linear issue id if known (HLB-XXX); null if unset."},
                "api_endpoints": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "Endpoints in 'METHOD /path' form mentioned by this entry.",
                },
                "db_tables": {
                    "type": "array",
                    "items": {"type": "string"},
                    "description": "DB tables mentioned by this entry.",
                },
                "metadata": {"type": "object"},
            },
        }
    },
}

with open(DB / "wiki-schema.json", "w", encoding="utf-8") as fh:
    json.dump(schema, fh, indent=2)
print(f"wrote {DB / 'wiki-schema.json'}")

print("\nSummary:")
print(f"  epics={len(epics)}, stories={len(stories)}, plans={len(plans)}")
print(f"  diagrams={len(diagrams)}, reviews={len(reviews)}, mockups={len(mockups)}")
print(f"  endpoints={len(endpoints)}, entities={len(entities)}")
print(f"  total entries={len(entries)}")
