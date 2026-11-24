"""tools/dev/changelog_fragment.py: the gate over changelog.d/ and the frozen [Unreleased], and
the tool that turns a branch's CHANGELOG lines into a fragment pair; and that a change of only
the changelogs starts no heavy job in any workflow."""

from __future__ import annotations

import datetime
import os
import re
import subprocess
import sys
from pathlib import Path

import pytest
import yaml

ROOT = Path(__file__).resolve().parents[3]
TOOL = ROOT / "tools/dev/changelog_fragment.py"
sys.path.insert(0, str(TOOL.parent))
sys.path.insert(0, str(ROOT / "tools/ci"))

import changelog_fragment as cf  # noqa: E402
import changelog_merge  # noqa: E402
import ci_paths  # noqa: E402

GOOD = {"2026-11-25-a.md": "### Fixed\n\n- **A.** x\n  more\n\n  a second paragraph\n",
        "2026-11-25-a.ar.md": "### أُصلح\n\n- **أ.** س\n  تتمة\n\n  فقرة ثانية\n"}  # fmt: skip


def bad(**files: str) -> str:
    """The errors of GOOD with the English (en) or Arabic (ar) replaced."""
    got = dict(GOOD)
    for lang, text in files.items():
        got["2026-11-25-a.md" if lang == "en" else "2026-11-25-a.ar.md"] = text
    return "\n".join(f"{f}: {e}" for f, e in cf.check_fragments(got))


# ---- the format -------------------------------------------------------------------------------


def test_a_good_pair_passes() -> None:
    assert cf.check_fragments(GOOD) == []


def test_the_repositorys_own_fragments_pass() -> None:
    files = {p.name: p.read_text(encoding="utf-8") for p in (ROOT / cf.DIR).iterdir() if p.name != "README.md"}
    assert cf.check_fragments(files) == []


@pytest.mark.parametrize(
    "name",
    ["fix.md", "2026-13-01-x.md", "2026-11-25.md", "2026-11-25-Fix.md", "2026-11-25-a--b.md",
     "2026-11-25-a.txt", "sub/2026-11-25-a.md", "2026-11-25-a.en.md"],
)  # fmt: skip
def test_a_bad_name_fails(name: str) -> None:
    assert "YYYY-MM-DD-slug.md" in cf.check_fragments({name: GOOD["2026-11-25-a.md"]})[0][1]


def test_one_language_alone_fails_naming_the_missing_file() -> None:
    (f, what), = cf.check_fragments({"2026-11-25-a.md": GOOD["2026-11-25-a.md"]})
    assert f == "2026-11-25-a.md" and "no changelog.d/2026-11-25-a.ar.md" in what
    (f, what), = cf.check_fragments({"2026-11-25-a.ar.md": GOOD["2026-11-25-a.ar.md"]})
    assert f == "2026-11-25-a.ar.md" and "no changelog.d/2026-11-25-a.md" in what


@pytest.mark.parametrize(
    ("files", "want"),
    [
        ({"en": "### Fixes\n\n- x\n"}, "line 1: '### Fixes' is not a section; write one of: ### Added"),
        ({"ar": "### Fixed\n\n- س\n"}, "write one of: ### أُضيف"),
        ({"en": "## Fixed\n\n- x\n"}, "is not a section"),
        ({"en": "- x\n"}, "an entry above any section"),
        ({"en": "### Fixed\n\nx\n"}, "line 3: 'x' is neither a section"),
        ({"en": "### Fixed\n\n- x\nnot indented\n"}, "indented by two spaces"),
        ({"en": "### Fixed\n\n<<<<<<< HEAD\n- x\n"}, "line 3: '<<<<<<< HEAD'"),
        ({"en": "### Fixed\n"}, "the section Fixed has no entry"),
        ({"en": "\n"}, "it is empty"),
        ({"en": "### Fixed\n\n- x\n\n### Fixed\n\n- y\n"}, "a second '### Fixed'"),
        ({"en": "### Fixed\n\n- x\n- y\n"}, "its entries are [Fixed: 1], the English has [Fixed: 2]"),
        ({"ar": "### أُضيف\n\n- س\n"}, "its entries are [Added: 1], the English has [Fixed: 1]"),
        ({"ar": "### أُصلح\n\n- left in English\n"}, "write it in Arabic"),
    ],
)
def test_a_malformed_fragment_says_what_to_write(files: dict[str, str], want: str) -> None:
    assert want in bad(**files)


def test_product_names_in_an_arabic_entry_pass() -> None:
    assert not bad(ar="### أُصلح\n\n- **مشغّل Windows وLinux.** صار يشغّل MKV وWebM كما هي عبر libmpv بلا تحويل\n")


def test_the_sections_are_the_portals() -> None:
    js = (ROOT / "web/packages/devportal/scripts/parity.mjs").read_text(encoding="utf-8")
    block = re.search(r"CHANGELOG_SECTIONS = \{(.*?)\};", js, re.DOTALL)
    assert block
    assert list(cf.SECTIONS.items()) == re.findall(r'(\w+): "([^"]+)"', block.group(1))
    for lang, heading in cf.UNRELEASED.items():
        assert f'{lang}: "{heading[3:]}"' in js
        assert f"\n{heading}\n" in (ROOT / cf.FILES[lang]).read_text(encoding="utf-8")


def test_the_template_the_gate_prints_is_a_good_pair() -> None:
    text = cf.template("2026-11-25-a")
    blocks = [re.sub(r"^    ", "", b, flags=re.MULTILINE) for b in re.findall(r"(?:^(?: {4}.*)?\n)+", text, re.MULTILINE)]
    en, ar = (b.strip() + "\n" for b in blocks if b.strip())
    assert cf.check_fragments({"2026-11-25-a.md": en, "2026-11-25-a.ar.md": ar}) == []


# ---- the merge's parts -----------------------------------------------------------------------


def test_the_parts_of_both_changelogs_are_the_files() -> None:
    for f in cf.FILES.values():
        text = (ROOT / f).read_text(encoding="utf-8")
        assert "".join(p if isinstance(p, str) else p.text for p in changelog_merge.parse(text).parts) == text


def test_an_entrys_second_paragraph_is_its_own() -> None:
    doc = changelog_merge.parse("### Fixed\n\n- a\n  b\n\n  c\n\n- d\n\nprose\n")
    assert [e.text for e in doc.entries()] == ["- a\n  b\n\n  c\n", "- d\n"]


def test_a_new_entry_above_an_edited_one_is_not_its_edit() -> None:
    base = changelog_merge.parse("### Fixed\n\n- **Old.** some words here\n")
    ours = changelog_merge.parse("### Fixed\n\n- **New.** quite different\n\n- **Old.** some words there\n")
    assert changelog_merge.replacements(base, ours) == {
        "- **Old.** some words here\n": "- **Old.** some words there\n"
    }


# ---- in a repository -------------------------------------------------------------------------


def changelog(lang: str, extra: str = "", top: str = "") -> str:
    h, s = (("[Unreleased]", "Fixed"), ("[لم يصدر بعد]", "أُصلح"))[lang == "ar"]
    old = ("old entry of some words", "مُدخل قديم من كلمات")[lang == "ar"]
    return f"# C\n\n## {h}\n{top}\n### {s}\n\n- {old}\n{extra}\n## [0.2.0]\n\n### {s}\n\n- first\n"


class Repo:
    def __init__(self, path: Path) -> None:
        self.path = path
        self.day = 0
        self.git("init", "-q", "-b", "main")
        for k, v in {"user.email": "test@example.invalid", "user.name": "t", "commit.gpgsign": "false"}.items():
            self.git("config", k, v)
        self.write(**{"CHANGELOG.md": changelog("en"), "CHANGELOG.ar.md": changelog("ar")})
        self.commit("start")

    def git(self, *args: str, check: bool = True) -> str:
        env = {**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_SYSTEM": os.devnull}
        for k in ("GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE"):
            env[k] = f"2026-11-{self.day + 1:02d}T12:00:00+0000"
        r = subprocess.run(["git", *args], cwd=self.path, env=env, capture_output=True, text=True, check=check)
        return r.stdout.strip()

    def write(self, **files: str) -> None:
        for f, text in files.items():
            p = self.path / f
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(text, encoding="utf-8")

    def commit(self, msg: str) -> str:
        self.git("add", "-A")
        self.git("commit", "-qm", msg)
        return self.git("rev-parse", "HEAD")

    def tool(self, *args: str) -> tuple[int, str]:
        r = subprocess.run([sys.executable, str(TOOL), *args], cwd=self.path, capture_output=True, text=True)
        return r.returncode, r.stdout + r.stderr

    def land(self) -> None:
        """The fragments land on main, after a day."""
        self.day += 1
        self.git("checkout", "-q", "main")
        self.write(**{cf.README: "x\n"})
        self.commit("fragments")
        self.day += 1

    def read(self, f: str) -> str:
        return (self.path / f).read_text(encoding="utf-8")


@pytest.fixture
def repo(tmp_path: Path) -> Repo:
    return Repo(tmp_path)


LINE = {"en": "\n- **New.** one\n  two\n", "ar": "\n- **جديد.** واحد\n  اثنان\n"}


def lines(r: Repo, branch: str = "topic/One", start: str = "main") -> None:
    r.git("checkout", "-q", "-b", branch, start)
    r.write(**{f: changelog(lang, LINE[lang]) for lang, f in cf.FILES.items()})
    r.commit("feat: new")


def check(r: Repo, head: str = "HEAD") -> tuple[int, str]:
    return r.tool("check", "--base", "main", "--head", head)


def test_a_new_branchs_changelog_line_is_refused_with_what_to_add(repo: Repo) -> None:
    repo.land()
    lines(repo)
    code, out = check(repo)
    assert code == 1
    assert "::error file=CHANGELOG.md::CHANGELOG.md: [Unreleased] is frozen" in out
    assert "::error file=CHANGELOG.ar.md::" in out
    assert "tools/dev/changelog_fragment.py convert" in out
    assert "-your-slug.md:" in out and "-your-slug.ar.md" in out and "### Fixed" in out and "### أُصلح" in out


def test_a_fragment_passes_in_its_place(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in GOOD.items()})
    repo.commit("feat")
    assert check(repo) == (0, "changelog fragments: 1 pair(s), in English and Arabic\n")


def test_a_malformed_fragment_fails_the_gate(repo: Repo) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/2026-11-25-a.md": "### Fixed\n\n- x\n"})
    code, out = check(repo)
    assert code == 1 and "::error file=changelog.d/2026-11-25-a.md::" in out and "no changelog.d/2026-11-25-a.ar.md" in out


def test_a_branch_begun_before_the_fragments_may_add_lines(repo: Repo) -> None:
    lines(repo)
    repo.land()
    repo.git("checkout", "-q", "topic/One")
    code, out = check(repo)
    assert code == 0 and "::warning file=CHANGELOG.md::" in out and "convert" in out
    # after it brings main in
    repo.git("merge", "-q", "--no-edit", "main")
    assert check(repo)[0] == 0
    # and after a rebase (the author date stays); and as CI reads it, by its head from elsewhere
    repo.git("rebase", "-q", "main")
    head = repo.git("rev-parse", "HEAD")
    repo.git("checkout", "-q", "main")
    assert check(repo, head)[0] == 0


def test_the_fragments_own_pull_request_is_not_asked(repo: Repo) -> None:
    lines(repo)  # main has no changelog.d yet (nor has a maintenance line, ever)
    assert check(repo)[0] == 0


def test_a_push_of_main_checks_the_fragments_alone(repo: Repo) -> None:
    repo.land()
    assert check(repo, "main")[0] == 0


def test_the_files_intro_and_releases_are_not_frozen(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"CHANGELOG.md": changelog("en").replace("# C\n", "# C\n\nAn intro.\n")})
    repo.commit("docs")
    assert check(repo)[0] == 0


def test_correcting_a_frozen_entry_needs_the_trailer_and_adds_none(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"CHANGELOG.md": changelog("en").replace("some words", "other words")})
    repo.commit("docs: correct")
    code, out = check(repo)
    assert code == 1 and "Changelog-Edit: <reason>" in out
    repo.git("commit", "-q", "--amend", "-m", "docs: correct\n\nChangelog-Edit: a wrong name")
    assert check(repo)[0] == 0
    repo.write(**{"CHANGELOG.md": changelog("en", LINE["en"])})
    repo.commit("feat: sneak one in")
    assert check(repo)[0] == 1


def pair(r: Repo, slug: str) -> tuple[str, str]:
    day = datetime.date.today().isoformat()
    return r.read(f"{cf.DIR}/{day}-{slug}.md"), r.read(f"{cf.DIR}/{day}-{slug}.ar.md")


def test_convert_moves_a_branchs_lines_and_the_gate_passes(repo: Repo) -> None:
    repo.land()
    lines(repo)
    code, out = repo.tool("convert", "--base", "main")
    assert code == 0, out
    assert pair(repo, "topic-one") == (
        "### Fixed\n\n- **New.** one\n  two\n",
        "### أُصلح\n\n- **جديد.** واحد\n  اثنان\n",
    )
    assert repo.git("diff", "--cached", "--name-only", "main", "--", "CHANGELOG.md", "CHANGELOG.ar.md") == ""
    repo.commit("a fragment")
    assert check(repo)[0] == 0
    # nothing is left to convert
    code, out = repo.tool("convert", "--base", "main")
    assert code == 0 and "nothing to convert" in out


def test_convert_keeps_several_sections_in_the_pages_order(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    tops = {"en": "\n### Security\n\n- s1\n\n  s1 more\n\n### Added\n\n- a1\n- a2\n",
            "ar": "\n### الأمان\n\n- أمان\n\n  تتمة\n\n### أُضيف\n\n- أول\n- ثان\n"}  # fmt: skip
    repo.write(**{f: changelog(lang, LINE[lang], tops[lang]) for lang, f in cf.FILES.items()})
    repo.commit("feat")
    code, out = repo.tool("convert", "--base", "main", "--slug", "my slug")
    assert code == 0 and "moved 4 entries" in out, out
    assert pair(repo, "my-slug")[0] == (
        "### Added\n\n- a1\n\n- a2\n\n### Fixed\n\n- **New.** one\n  two\n\n### Security\n\n- s1\n\n  s1 more\n"
    )
    assert repo.read("CHANGELOG.md") == changelog("en")


def test_convert_keeps_an_edit_of_an_entry_where_it_is(repo: Repo) -> None:
    lines(repo)
    for lang, f in cf.FILES.items():
        old, new = (("some words", "other words"), ("كلمات", "ألفاظ"))[lang == "ar"]
        repo.write(**{f: repo.read(f).replace(old, new)})
    repo.commit("fix the old entry")
    repo.land()
    repo.git("checkout", "-q", "topic/One")
    repo.git("merge", "-q", "--no-edit", "main")
    assert repo.tool("convert", "--base", "main")[0] == 0
    assert repo.read("CHANGELOG.md") == changelog("en").replace("some words", "other words")
    assert "other words" not in pair(repo, "topic-one")[0]


def test_convert_never_overwrites_a_fragment(repo: Repo) -> None:
    repo.land()
    lines(repo)
    day = datetime.date.today().isoformat()
    repo.write(**{f"{cf.DIR}/{day}-topic-one.ar.md": "mine"})
    assert repo.tool("convert", "--base", "main")[0] == 0
    assert pair(repo, "topic-one-2")[0].startswith("### Fixed")
    assert repo.read(f"{cf.DIR}/{day}-topic-one.ar.md") == "mine"


@pytest.mark.parametrize(
    ("files", "want"),
    [
        ({"CHANGELOG.md": changelog("en", LINE["en"])}, "adds [Fixed: 1] to CHANGELOG.md and [none] to CHANGELOG.ar.md"),
        ({"CHANGELOG.md": changelog("en").replace("# C\n", "# C\n\nNew prose.\n")}, "text that is not an entry"),
        ({"CHANGELOG.md": changelog("en") + "\n- late\n"}, "a new entry under ## [0.2.0]"),
        ({"CHANGELOG.md": changelog("en", top="\n### Fixes\n\n- x\n")}, "a new entry under ## [Unreleased] / ### Fixes"),
    ],
)  # fmt: skip
def test_convert_stops_writing_nothing_when_a_person_must_look(repo: Repo, files: dict[str, str], want: str) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**files)
    repo.commit("feat")
    code, out = repo.tool("convert", "--base", "main")
    assert code == 3 and want in out, out
    assert repo.git("status", "--porcelain") == ""


def test_convert_in_a_merge_takes_mains_files(repo: Repo) -> None:
    lines(repo)
    repo.land()
    main = {"en": "\n- **Main.** x\n", "ar": "\n- **الرئيسي.** س\n"}
    repo.write(**{f: changelog(lang, main[lang]) for lang, f in cf.FILES.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "topic/One")
    repo.git("merge", "main", check=False)
    assert "<<<<<<<" in repo.read("CHANGELOG.md")
    assert repo.tool("convert")[0] == 3  # a merge is in progress: merge-main.sh finishes it
    code, out = repo.tool("convert", "--merge")
    assert code == 0, out
    assert repo.read("CHANGELOG.md") == changelog("en", main["en"])
    assert repo.read("CHANGELOG.ar.md") == changelog("ar", main["ar"])
    assert pair(repo, "topic-one")[0] == "### Fixed\n\n- **New.** one\n  two\n"
    assert repo.git("diff", "--name-only", "--diff-filter=U") == ""


def test_convert_in_a_merge_stops_when_both_sides_changed_an_entry(repo: Repo) -> None:
    lines(repo)
    repo.write(**{"CHANGELOG.md": repo.read("CHANGELOG.md").replace("some words", "our words")})
    repo.commit("ours")
    repo.land()
    repo.write(**{"CHANGELOG.md": changelog("en").replace("some words", "their words")})
    repo.commit("theirs")
    repo.git("checkout", "-q", "topic/One")
    repo.git("merge", "main", check=False)
    code, out = repo.tool("convert", "--merge")
    assert code == 3 and "main and this branch changed the same entry" in out
    assert "<<<<<<<" in repo.read("CHANGELOG.md")


# ---- what a changelog change runs -------------------------------------------------------------

PATHS = ["CHANGELOG.md", "CHANGELOG.ar.md", "changelog.d/2026-11-25-a.md", "changelog.d/2026-11-25-a.ar.md"]


@pytest.mark.parametrize("files", [PATHS, PATHS[:2], PATHS[2:]])
@pytest.mark.parametrize("event", ["pull_request", "branch"])
def test_a_changelog_change_runs_the_portals_job_alone(files: list[str], event: str) -> None:
    runs = ci_paths.which(files, event=event)
    got = {j for j, r in runs.items() if r is not None}
    light = {"changes", "lint", "pr-body-check", "affected-tests", "ci-success", "devportal"}
    assert "devportal" in got and got <= light, sorted(got - light)
    lint = runs["lint"]
    assert lint is not None and not any(v is True for v in lint.inputs.values())


def test_adding_a_fragment_to_a_code_change_adds_the_portals_job_alone() -> None:
    for code in (["apps/apple/Shared/x.swift"], ["web/src/App.tsx"], ["api/main.go"]):
        a, b = (
            {j for j, r in ci_paths.which(f).items() if r is not None} for f in (code, [*code, *PATHS])
        )
        assert b == a | {"devportal"}


def test_no_other_workflow_starts_on_a_changelog_change() -> None:
    """Every workflow with its own pull_request trigger: its paths match no changelog file, or it
    is one that runs on every pull request."""
    everywhere = {"ci.yml", "labeler.yml", "reference-images.yml"}
    for wf in sorted((ROOT / ".github/workflows").glob("*.yml")):
        on = yaml.safe_load(wf.read_text(encoding="utf-8")).get(True)
        pr = on.get("pull_request", on.get("pull_request_target")) if isinstance(on, dict) else None
        if not isinstance(on, dict) or not {"pull_request", "pull_request_target"} & set(on) or wf.name in everywhere:
            continue
        assert pr and pr.get("paths"), f"{wf.name} runs on every pull request"
        hits = [g for g in pr["paths"] if not g.startswith("!") and any(ci_paths.matches(g, p) for p in PATHS)]
        assert not hits, f"{wf.name} starts on a changelog change: {hits}"


def test_the_portal_deploys_on_what_its_filter_names() -> None:
    filters = ci_paths.load_filters()
    want = [g for g in filters["devportal"] if g not in filters["_ci"]]
    deploy = yaml.safe_load((ROOT / ".github/workflows/devportal-deploy.yml").read_text(encoding="utf-8"))[True]
    assert [g for g in deploy["push"]["paths"]] == want
    assert "changelog.d/**" in want


def test_the_lint_job_runs_the_gate_on_the_pull_requests_own_head() -> None:
    jobs = yaml.safe_load((ROOT / ".github/workflows/_lint.yml").read_text(encoding="utf-8"))["jobs"]
    repo_job = jobs["repo"]
    assert repo_job["steps"][0]["with"]["fetch-depth"] == 0
    step, = (s for s in repo_job["steps"] if "changelog_fragment.py check" in s.get("run", ""))
    assert "github.event.pull_request.head.sha || github.sha" in step["env"]["HEAD"]
    assert "github.base_ref" in step["env"]["BASE"]


def test_the_portal_reads_the_release_tags() -> None:
    steps = yaml.safe_load((ROOT / ".github/workflows/_devportal.yml").read_text(encoding="utf-8"))["jobs"]["devportal"]["steps"]
    names = [s.get("name") for s in steps]
    assert names.index("The release tags") < names.index("Build")
    assert "refs/tags/v*" in steps[names.index("The release tags")]["run"]
