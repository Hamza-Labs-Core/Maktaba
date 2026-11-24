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


def test_the_most_alike_of_two_new_entries_is_the_edit() -> None:
    old = "- **Old.** one two three four five six\n"
    near, far = old.replace("six", "seven"), "- **Old.** one two three four new words\n"
    for a, b in ((near, far), (far, near)):
        got = changelog_merge.replacements(changelog_merge.parse(old), changelog_merge.parse(a + b))
        assert got == {old: near}


def test_an_unlike_entry_in_a_removed_ones_place_is_not_its_edit() -> None:
    base, ours = (changelog_merge.parse(x) for x in ("- **Old.** some words here\n", "- **New.** quite different\n"))
    assert changelog_merge.replacements(base, ours) == {}


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


def test_a_branch_without_the_tool_is_told_to_bring_main_in_first(repo: Repo) -> None:
    lines(repo, "old")
    repo.land()
    lines(repo, "new", "main~1")  # begun after the fragments landed, from a main without them
    code, out = check(repo)
    assert code == 1 and f"This branch has no {cf.TOOL} yet: bring main in first (tools/dev/merge-main.sh)" in out
    repo.git("checkout", "-q", "old")
    code, out = check(repo)
    assert code == 0 and "bring main in first" in out
    repo.write(**{cf.TOOL: "x"})
    repo.commit("the tool")
    code, out = check(repo)
    assert code == 0 and "bring main in first" not in out and f"`{cf.CONVERT}` moves" in out


def test_an_out_of_date_branch_is_compared_with_where_it_left_main(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"x.txt": "x"})
    repo.commit("feat")
    # main's [Unreleased] and fragments move on (an older branch's lines, others' fragments)
    repo.git("checkout", "-q", "main")
    repo.write(**{f: changelog(lang, LINE[lang]) for lang, f in cf.FILES.items()})
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in GOOD.items()})
    repo.commit("main moves")
    assert check(repo, "topic")[0] == 0
    repo.git("checkout", "-q", "topic")
    assert check(repo)[0] == 0


def test_the_fragments_landed_when_their_merge_did_not_when_they_were_written(repo: Repo) -> None:
    repo.git("checkout", "-q", "-b", "fragments")
    repo.write(**{cf.README: "x\n"})
    repo.commit("fragments, written first")
    repo.day += 1
    lines(repo, "between")
    repo.day += 1
    repo.git("checkout", "-q", "main")
    repo.git("merge", "-q", "--no-ff", "--no-edit", "fragments")
    code, out = check(repo, "between")
    assert code == 0 and "::warning" in out


def test_a_hidden_file_is_no_fragment(repo: Repo) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/.DS_Store": "x"})
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


@pytest.mark.parametrize("merging", [False, True])
@pytest.mark.parametrize("squash", [False, True])
def test_stacked_branches_move_a_shared_entry_once(repo: Repo, squash: bool, merging: bool) -> None:
    lines(repo, "first")
    repo.git("checkout", "-q", "-b", "second")
    repo.write(**{f: repo.read(f).replace(LINE[lang], LINE[lang] + "\n- " + ("second", "ثانٍ")[lang == "ar"] + "\n")
                  for lang, f in cf.FILES.items()})  # fmt: skip
    repo.commit("feat: second")
    repo.land()
    # the first moves its entry and merges
    repo.git("checkout", "-q", "first")
    repo.git("merge", "-q", "--no-edit", "main")
    assert repo.tool("convert", "--base", "main")[0] == 0
    repo.commit("a fragment")
    repo.git("checkout", "-q", "main")
    repo.git("merge", "-q", "--squash", "first") if squash else repo.git("merge", "-q", "--no-ff", "--no-edit", "first")
    main = {"en": "\n- main\n", "ar": "\n- الرئيسي\n"}
    repo.write(**{f: changelog(lang, main[lang]) for lang, f in cf.FILES.items()})
    repo.commit("another")
    repo.git("checkout", "-q", "second")
    if merging:
        repo.git("merge", "--no-commit", "main", check=False)
        code, out = repo.tool("convert", "--merge")
    else:
        code, out = repo.tool("convert", "--base", "main")
    assert code == 0 and "moved 1 entry" in out, out
    assert pair(repo, "second") == ("### Fixed\n\n- second\n", "### أُصلح\n\n- ثانٍ\n")
    repo.git("commit", "-qam", "a fragment")
    if not merging:
        repo.git("merge", "-q", "--no-edit", "main")
    for lang, f in cf.FILES.items():
        assert repo.read(f) == changelog(lang, main[lang])
    files = {q.name: q.read_text(encoding="utf-8") for q in (repo.path / cf.DIR).iterdir() if q.name != "README.md"}
    assert len(files) == 4 and cf.check_fragments(files) == []


def test_a_branch_whose_every_entry_is_in_a_fragment_only_loses_its_lines(repo: Repo) -> None:
    repo.land()
    lines(repo)
    day = datetime.date.today().isoformat()
    repo.write(**{f"{cf.DIR}/{day}-x.md": "### Fixed\n" + LINE["en"], f"{cf.DIR}/{day}-x.ar.md": "### أُصلح\n" + LINE["ar"]})
    code, out = repo.tool("convert", "--base", "main")
    assert code == 0 and "no entry to move" in out, out
    assert repo.read("CHANGELOG.md") == changelog("en")


TWICE = {**GOOD, **{f.replace("-25-", "-26-"): t for f, t in GOOD.items()}}


def test_an_entry_a_branch_adds_to_a_second_fragment_is_an_error() -> None:
    errors = cf.introduced(GOOD, TWICE)
    assert [f for f, _ in errors] == ["2026-11-26-a.ar.md", "2026-11-26-a.md"]
    assert "an entry that changelog.d/2026-11-25-a.md has" in errors[1][1]
    assert len(cf.introduced({}, TWICE)) == 2


def test_an_entry_that_was_in_two_fragments_already_is_not() -> None:
    assert cf.introduced(TWICE, TWICE) == []
    assert cf.introduced(TWICE, {**TWICE, "2026-11-27-b.md": "### Added\n\n- b\n"}) == []
    assert cf.introduced(TWICE, GOOD) == []
    assert cf.check_fragments(TWICE) == []


class Stack:
    """Two stacked branches, "first" and "second" (first's entry and its own), each converted on
    main before either merged, in fragments of their own names."""

    def __init__(self, r: Repo) -> None:
        self.r = r
        lines(r, "first")
        r.git("checkout", "-q", "-b", "second")
        for lang, f in cf.FILES.items():
            more = "\n- " + ("second", "ثانٍ")[lang == "ar"] + "\n"
            r.write(**{f: r.read(f).replace(LINE[lang], LINE[lang] + more)})
        r.commit("feat: second")
        r.land()
        for b in ("first", "second"):
            r.git("checkout", "-q", b)
            r.git("merge", "-q", "--no-edit", "main")
            assert r.tool("convert", "--base", "main")[0] == 0
            r.commit("a fragment")
            assert check(r)[0] == 0, b

    def merge(self, b: str) -> None:
        self.r.git("checkout", "-q", "main")
        self.r.git("merge", "-q", "--no-ff", "--no-edit", b)

    def merge_main(self, b: str) -> str:
        self.r.git("checkout", "-q", b)
        out = subprocess.run(["bash", str(ROOT / "tools/dev/merge-main.sh"), "main"], cwd=self.r.path,
                             capture_output=True, text=True)  # fmt: skip
        assert out.returncode == 0, out.stdout + out.stderr
        assert self.r.git("status", "--porcelain") == ""
        return out.stdout

    def entries(self) -> list[str]:
        files = {p.name: p.read_text(encoding="utf-8") for p in (self.r.path / cf.DIR).iterdir() if p.name != "README.md"}
        assert cf.check_fragments(files) == []
        return sorted(e for n, t in files.items() if not n.endswith(".ar.md") for e in cf.entries_of(n, t))


NEW = LINE["en"].strip("\n")
ORDERS = [("first", "second"), ("second", "first")]


@pytest.mark.parametrize("order", ORDERS)
def test_two_stacked_branches_that_both_converted_turn_nothing_red(repo: Repo, order: tuple[str, str]) -> None:
    st = Stack(repo)
    repo.git("checkout", "-q", "-b", "unrelated", "main")
    repo.write(**{"notes.txt": "x"})
    repo.commit("unrelated")
    for b in order:
        st.merge(b)
        assert check(repo, "main")[0] == 0  # main itself
        assert check(repo, "unrelated")[0] == 0
        for other in order:
            assert check(repo, other)[0] == 0, other
        # as CI reads a pull request: its merge with main checked out, the branch's own head named
        repo.git("checkout", "-q", "--detach", "main")
        repo.git("merge", "-q", "--no-ff", "--no-edit", "unrelated")
        assert check(repo, "unrelated")[0] == 0
    assert st.entries() == [NEW, NEW, "- second"]  # on main, twice: the page shows it once
    # a branch that begins on that main, or brings it in, is not asked either
    repo.git("checkout", "-q", "unrelated")
    st.merge_main("unrelated")
    assert check(repo)[0] == 0
    repo.write(**{"notes.txt": "y"})
    repo.commit("more")
    assert check(repo)[0] == 0


@pytest.mark.parametrize("order", ORDERS)
def test_bringing_main_in_drops_what_mains_fragments_have(repo: Repo, order: tuple[str, str]) -> None:
    st = Stack(repo)
    st.merge(order[0])
    # merged by hand, the gate sees the branch hold the entry twice
    repo.git("checkout", "-q", order[1])
    repo.git("checkout", "-q", "-b", "by-hand")
    repo.git("merge", "-q", "--no-edit", "main")
    code, out = check(repo)
    assert code == 1 and "it has an entry that changelog.d/" in out
    # convert drops it
    assert repo.tool("convert", "--base", "main")[0] == 0
    repo.git("commit", "-qm", "once")
    assert check(repo)[0] == 0 and st.entries() == [NEW, "- second"]
    # and merge-main.sh does it all
    out = st.merge_main(order[1])
    assert "dropped 1 entry" in out
    assert check(repo)[0] == 0 and st.entries() == [NEW, "- second"]
    st.merge(order[1])
    assert check(repo, "main")[0] == 0 and st.entries() == [NEW, "- second"]


def test_bringing_main_in_leaves_a_branchs_other_fragments_and_mains_alone(repo: Repo) -> None:
    st = Stack(repo)
    st.merge("first")
    before = repo.git("rev-parse", "main:changelog.d")
    repo.git("checkout", "-q", "-b", "other", "main~1")
    repo.write(**{f"{cf.DIR}/2026-11-20-mine.md": "### Added\n\n- mine\n", f"{cf.DIR}/2026-11-20-mine.ar.md": "### أُضيف\n\n- لي\n"})
    repo.commit("feat: mine")
    assert "dropped" not in st.merge_main("other")
    assert st.entries() == [NEW, "- mine"]
    assert repo.git("rev-parse", "main:changelog.d") == before


def test_a_child_that_merged_its_converted_parent_drops_the_parents_entries(repo: Repo) -> None:
    st = Stack(repo)
    repo.git("checkout", "-q", "second")
    repo.git("merge", "-q", "--no-edit", "first")
    assert check(repo)[0] == 1
    assert repo.tool("convert", "--base", "main")[0] == 0
    repo.git("commit", "-qm", "once")
    assert check(repo)[0] == 0 and st.entries() == [NEW, "- second"]
    assert repo.git("diff", "--name-only", "first", "--", cf.DIR).count("first") == 0  # the parent's pair is as it was


def test_an_entry_half_there_is_not_dropped(repo: Repo) -> None:
    """Dropped only when both languages are there: never one half of a pair."""
    repo.land()
    en, ar = GOOD.values()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in GOOD.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "-b", "topic", "main~1")
    repo.write(**{f"{cf.DIR}/2026-11-26-b.md": en, f"{cf.DIR}/2026-11-26-b.ar.md": "### أُصلح\n\n- آخر\n"})
    repo.commit("feat")
    code, out = repo.tool("prune", "--base", "main")
    assert code == 0 and out == "" and repo.git("status", "--porcelain") == ""


def test_a_branch_out_of_date_with_a_cleaned_up_main_is_not_asked(repo: Repo) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in TWICE.items()})
    repo.commit("twice on main")
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"x.txt": "x"})
    repo.commit("feat")
    repo.git("checkout", "-q", "main")
    repo.git("rm", "-q", *(f"{cf.DIR}/{f}" for f in GOOD))
    repo.commit("once")
    repo.git("checkout", "-q", "topic")
    assert check(repo)[0] == 0


@pytest.mark.parametrize("side", ["--ours", "--theirs"])
def test_a_side_taken_by_hand_stays(repo: Repo, side: str) -> None:
    lines(repo)
    repo.land()
    repo.write(**{f: changelog(lang, ("\n- main\n", "\n- الرئيسي\n")[lang == "ar"]) for lang, f in cf.FILES.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "topic/One")
    repo.git("merge", "main", check=False)
    repo.git("checkout", side, "CHANGELOG.md")
    repo.git("add", "CHANGELOG.md")
    taken = repo.read("CHANGELOG.md")
    code, out = repo.tool("convert", "--merge")
    assert code == 3 and "CHANGELOG.md was resolved by hand" in out
    assert repo.read("CHANGELOG.md") == taken


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


def test_a_failed_fetch_of_the_tags_fails_the_portals_job(tmp_path: Path) -> None:
    steps = yaml.safe_load((ROOT / ".github/workflows/_devportal.yml").read_text(encoding="utf-8"))["jobs"]["devportal"]["steps"]
    step, = (s for s in steps if s.get("name") == "The release tags")
    assert "continue-on-error" not in step and "if" not in step
    for name, body in {"sleep": "exit 0", "git": 'echo x >>"$CALLS"; [ "$(wc -l <"$CALLS")" -ge "$GOOD" ]'}.items():
        (tmp_path / name).write_text(f"#!/bin/bash\n{body}\n")
        (tmp_path / name).chmod(0o755)
    for good, want, calls in (("1", 0, 1), ("3", 0, 3), ("4", 1, 3)):
        log = tmp_path / f"calls{good}"
        env = {"PATH": f"{tmp_path}:{os.environ['PATH']}", "CALLS": str(log), "GOOD": good}
        r = subprocess.run(["bash", "-e", "-c", step["run"]], env=env, capture_output=True, text=True)
        assert (r.returncode, log.read_text().count("x")) == (want, calls), (good, r.stdout)
