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


def test_the_changelog_intro_is_not_frozen(repo: Repo) -> None:
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


@pytest.mark.parametrize("lang", ["en", "ar"])
def test_released_sections_refuse_new_entries_even_with_the_trailer(repo: Repo, lang: str) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    f = cf.FILES[lang]
    repo.write(**{f: repo.read(f) + LINE[lang]})
    repo.commit("add to a release\n\nChangelog-Edit: correct a typo")
    code, out = check(repo)
    assert code == 1 and "[0.2.0] is frozen" in out
    assert cf.CONVERT not in out


@pytest.mark.parametrize("lang", ["en", "ar"])
@pytest.mark.parametrize("released", [False, True])
def test_a_trailer_does_not_allow_replacing_an_entry_with_an_unrelated_one(
    repo: Repo, lang: str, released: bool
) -> None:
    f = cf.FILES[lang]
    original = ("old entry of some words", "مُدخل قديم من كلمات")[lang == "ar"]
    if released:
        repo.write(**{f: repo.read(f).replace("- first", f"- {original} in a release")})
        repo.commit("longer release entry")
        original += " in a release"
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f: repo.read(f).replace(original, "an unrelated addition كل شيء مختلف")})
    repo.commit("swap an entry\n\nChangelog-Edit: correct a typo")
    assert check(repo)[0] == 1


def test_a_list_marker_does_not_make_two_short_entries_a_match(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"CHANGELOG.md": repo.read("CHANGELOG.md").replace("- first", "- unrelated")})
    repo.commit("swap a short entry\n\nChangelog-Edit: correct a typo")
    assert check(repo)[0] == 1


def test_a_released_entry_can_be_corrected(repo: Repo) -> None:
    repo.write(**{"CHANGELOG.md": repo.read("CHANGELOG.md").replace("- first", "- first release with a player")})
    repo.commit("release entry")
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"CHANGELOG.md": repo.read("CHANGELOG.md").replace("with a player", "with the player")})
    repo.commit("correct a released entry\n\nChangelog-Edit: clarify the description")
    assert check(repo)[0] == 0


@pytest.mark.parametrize("rename", [False, True])
def test_removing_a_committed_fragment_needs_a_trailer(repo: Repo, rename: bool) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/{name}": body for name, body in GOOD.items()})
    repo.commit("fragment")
    repo.git("tag", "v0.3.0")
    repo.git("checkout", "-q", "-b", "topic")
    for name in GOOD:
        if rename:
            repo.git("mv", f"{cf.DIR}/{name}", f"{cf.DIR}/{name.replace('-a.', '-b.')}")
        else:
            repo.git("rm", f"{cf.DIR}/{name}")
    repo.commit("remove old filename")
    code, out = check(repo)
    assert code == 1 and "removing or renaming a committed fragment needs" in out
    repo.git("commit", "--amend", "-qm", "remove old filename\n\nChangelog-Edit: retire obsolete entry")
    assert check(repo)[0] == 0


def test_a_repeated_entry_inside_one_fragment_fails_the_gate(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f"{cf.DIR}/{name}": body + "\n" + body.split("\n\n", 1)[1] for name, body in GOOD.items()})
    repo.commit("repeat")
    code, out = check(repo)
    assert code == 1 and "the section Fixed repeats entry" in out


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


@pytest.mark.parametrize(
    ("edit", "want"),
    [
        (lambda text: text.replace("### Fixed\n\n- old", "### Added\n\n- old"), "moved between sections"),
        (lambda text: text.replace("old entry of some words", "complete rewrite"), "without a matching edit"),
    ],
)
def test_convert_leaves_moved_or_rewritten_entries_for_review(repo: Repo, edit, want: str) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"CHANGELOG.md": edit(repo.read("CHANGELOG.md"))})
    repo.commit("edit")
    code, out = repo.tool("convert", "--base", "main")
    assert code == 3 and want in out
    assert repo.git("status", "--porcelain") == ""


def test_convert_preserves_a_resolution_with_multiple_merge_bases(repo: Repo) -> None:
    repo.land()
    repo.git("branch", "side")
    repo.write(**{"CHANGELOG.md": changelog("en", "\n- main\n")})
    main = repo.commit("main entry")
    repo.git("checkout", "-q", "side")
    repo.write(**{"CHANGELOG.md": changelog("en", "\n- side\n")})
    side = repo.commit("side entry")
    # Both tips merged the other's former tip, producing two common ancestors.
    for branch, other, msg in [("main", side, "main merged side"), ("side", main, "side merged main")]:
        repo.git("checkout", "-q", branch)
        repo.git("merge", "--no-ff", "-s", "ours", "-m", msg, other)
    assert set(repo.git("merge-base", "--all", "main", "side").splitlines()) == {main, side}
    # Create a merge in progress and leave an explicit resolution in the index.
    repo.git("revert", "--no-edit", side)
    repo.git("merge", "--no-commit", "--no-ff", "main", check=False)
    assert repo.git("rev-parse", "MERGE_HEAD")
    repo.write(**{"CHANGELOG.md": changelog("en", "\n- manually resolved\n")})
    repo.git("add", "CHANGELOG.md")
    staged = repo.git("diff", "--cached")
    code, out = repo.tool("convert", "--merge")
    assert code == 3 and "different contents at multiple merge bases" in out
    assert repo.git("diff", "--cached") == staged


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
    # a squash the branch has not brought in is not in its tree: it moves the shared entry too
    twice = squash and not merging
    assert code == 0 and ("moved 2 entries" if twice else "moved 1 entry") in out, out
    if not twice:
        assert pair(repo, "second") == ("### Fixed\n\n- second\n", "### أُصلح\n\n- ثانٍ\n")
    repo.git("commit", "-qam", "a fragment")
    if not merging:
        repo.git("merge", "-q", "--no-edit", "main")
    for lang, f in cf.FILES.items():
        assert repo.read(f) == changelog(lang, main[lang])
    files = {q.name: q.read_text(encoding="utf-8") for q in (repo.path / cf.DIR).iterdir() if q.name != "README.md"}
    assert len(files) == 4 and cf.check_fragments(files) == []
    assert check(repo)[0] == 0


def test_a_branch_whose_every_entry_is_in_a_fragment_only_loses_its_lines(repo: Repo) -> None:
    repo.land()
    lines(repo)
    day = datetime.date.today().isoformat()
    repo.write(**{f"{cf.DIR}/{day}-x.md": "### Fixed\n" + LINE["en"], f"{cf.DIR}/{day}-x.ar.md": "### أُصلح\n" + LINE["ar"]})
    repo.git("add", cf.DIR)
    code, out = repo.tool("convert", "--base", "main")
    assert code == 0 and "no entry to move" in out, out
    assert repo.read("CHANGELOG.md") == changelog("en")


TWICE = {**GOOD, **{f.replace("-25-", "-26-"): t for f, t in GOOD.items()}}


def test_the_same_entry_in_two_fragments_the_branch_adds_is_an_error(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in TWICE.items()})
    for _ in range(2):  # not committed yet, and committed
        code, out = check(repo)
        assert code == 1 and "::error file=changelog.d/2026-11-26-a.md::" in out
        assert "is in changelog.d/2026-11-25-a.md too, which this branch also adds: keep one" in out
        repo.git("add", "-A")
        repo.git("commit", "-q", "--allow-empty", "-m", "feat")


def test_two_distinct_repeats_between_the_same_fragments_are_reported_in_english(repo: Repo) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    files = {
        f"{cf.DIR}/{name}": body + ("\n- A second entry\n" if not name.endswith(".ar.md") else "\n- مُدخل ثانٍ\n")
        for name, body in TWICE.items()
    }
    repo.write(**files)
    code, out = check(repo)
    assert code == 1
    errors = [line for line in out.splitlines() if line.startswith("::error")]
    assert len(errors) == 2
    for entry in ("- **A.** x", "- A second entry"):
        assert any(line.startswith("::error file=changelog.d/2026-11-26-a.md::") and repr(entry) in line for line in errors)


@pytest.mark.parametrize(("en", "ar", "duplicates"), [
    ("### Added\n\n- **A.** x\n", "### أُضيف\n\n- **أ.** س\n", False),
    ("### Fixed\n\n- **A.** x\n", "### أُصلح\n\n- **أ.** غيره\n", True),
    ("### Fixed\n\n- **A.** y\n", "### أُصلح\n\n- **أ.** س\n", True),
])
def test_a_matching_translation_identifies_the_same_entry(repo: Repo, en: str, ar: str, duplicates: bool) -> None:
    repo.land()
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f"{cf.DIR}/2026-11-25-a.md": "### Fixed\n\n- **A.** x\n",
                  f"{cf.DIR}/2026-11-25-a.ar.md": "### أُصلح\n\n- **أ.** س\n",
                  f"{cf.DIR}/2026-11-26-a.md": en, f"{cf.DIR}/2026-11-26-a.ar.md": ar})  # fmt: skip
    code, out = check(repo)
    assert code == int(duplicates)
    assert ("which this branch also adds" in out) == duplicates
    if duplicates:
        suffix = ".ar.md" if "**A.** y" in en else ".md"
        assert f"::error file=changelog.d/2026-11-26-a{suffix}::" in out


def test_an_entry_main_has_is_a_warning_with_nothing_to_do(repo: Repo) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in GOOD.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in TWICE.items()})
    repo.commit("feat: say it again")
    code, out = check(repo)
    assert code == 0 and "::warning file=changelog.d/2026-11-26-a.md::" in out
    assert "there is nothing to do" in out and "keep one" not in out and "rop" not in out
    assert "::" not in check(repo, "main")[1]


def test_an_entry_that_was_in_two_fragments_already_says_nothing(repo: Repo) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in TWICE.items()})
    repo.commit("twice on main")
    repo.git("checkout", "-q", "-b", "topic")
    repo.write(**{"x.txt": "x"})
    repo.commit("feat")
    code, out = check(repo)
    assert code == 0 and "::" not in out


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
        out = merge_main(self.r)
        assert out.returncode == 0, out.stdout + out.stderr
        assert self.r.git("status", "--porcelain") == ""
        return out.stdout

    def entries(self) -> list[str]:
        files = {p.name: p.read_text(encoding="utf-8") for p in (self.r.path / cf.DIR).iterdir() if p.name != "README.md"}
        assert cf.check_fragments(files) == []
        return sorted(en for (_, en, _), names in cf.pairs_of(files).items() for _ in names)

    def fragments(self, rev: str) -> dict[str, str]:
        """A branch's fragments: name -> blob."""
        rows = (row.split() for row in self.r.git("ls-tree", rev, f"{cf.DIR}/").splitlines())
        return {name: blob for _, _, blob, name in rows}


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
def test_bringing_main_in_and_converting_change_no_fragment(repo: Repo, order: tuple[str, str]) -> None:
    st = Stack(repo)
    st.merge(order[0])
    own = st.fragments(order[1])
    commits = repo.git("rev-list", "--count", order[1])
    st.merge_main(order[1])
    assert repo.git("log", "-1", "--format=%P").count(" ") == 1  # the merge, and no commit of the script's own after it
    assert int(repo.git("rev-list", "--count", "--first-parent", "HEAD")) == int(
        repo.git("rev-list", "--count", "--first-parent", "HEAD~1")) + 1  # fmt: skip
    assert int(commits) < int(repo.git("rev-list", "--count", "HEAD"))
    for _ in range(2):  # and again after convert, which has nothing to do
        assert st.fragments("HEAD").items() >= {**own, **st.fragments("main")}.items()
        code, out = check(repo)
        assert code == 0 and "::warning" in out and "there is nothing to do" in out
        code, out = repo.tool("convert", "--base", "main")
        assert code == 0 and "nothing to convert" in out
        assert repo.git("status", "--porcelain") == ""
    st.merge(order[1])
    assert check(repo, "main")[0] == 0 and st.entries() == [NEW, NEW, "- second"]


def test_a_child_that_merged_its_converted_parent_is_only_warned(repo: Repo) -> None:
    st = Stack(repo)
    repo.git("checkout", "-q", "second")
    repo.git("merge", "-q", "--no-edit", "first")
    code, out = check(repo)
    assert code == 0 and "::warning" in out and "which is not this branch's" in out
    code, out = repo.tool("convert", "--base", "main")
    assert code == 0 and "nothing to convert" in out
    assert repo.git("status", "--porcelain") == "" and st.entries() == [NEW, NEW, "- second"]


def test_a_rebased_child_can_be_checked_against_its_converted_parent(repo: Repo) -> None:
    st = Stack(repo)
    repo.git("checkout", "-q", "-b", "rebased-child", "first")
    for name in st.fragments("second").keys() - st.fragments("first").keys():
        repo.git("checkout", "second", "--", name)
    repo.commit("rebased conversion")
    assert st.entries() == [NEW, NEW, "- second"]
    assert check(repo)[0] == 1
    code, out = repo.tool("check", "--base", "first")
    assert code == 0 and "::warning" in out


def merge_main(r: Repo) -> subprocess.CompletedProcess[str]:
    return subprocess.run(["bash", str(ROOT / "tools/dev/merge-main.sh"), "main"], cwd=r.path, capture_output=True, text=True)


@pytest.mark.parametrize("moves", [False, True])
def test_a_renamed_fragment_is_never_touched(repo: Repo, moves: bool) -> None:
    """The branch's rename leaves it the only copy; main's old name is no other one."""
    repo.land()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in GOOD.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "-b", "topic")
    for f in GOOD:
        repo.git("mv", f"{cf.DIR}/{f}", f"{cf.DIR}/{f.replace('-a.', '-better.')}")
    repo.commit("a better name")
    if moves:
        repo.git("checkout", "-q", "main")
        repo.write(**{"x.txt": "x"})
        repo.commit("main moves")
        repo.git("checkout", "-q", "topic")
    tree = repo.git("rev-parse", "HEAD:changelog.d")
    assert merge_main(repo).returncode == 0
    assert repo.tool("convert", "--base", "main")[0] == 0
    assert repo.git("rev-parse", "HEAD:changelog.d") == tree
    assert repo.git("status", "--porcelain") == ""
    code, out = check(repo)
    assert code == 1 and "removing or renaming a committed fragment needs" in out


@pytest.mark.parametrize("state", ["up to date", "behind", "diverged"])
def test_merge_main_never_commits_what_a_person_staged(repo: Repo, state: str) -> None:
    repo.land()
    repo.write(**{f"{cf.DIR}/{f}": t for f, t in TWICE.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "-b", "topic")
    if state == "diverged":
        repo.write(**{"own.txt": "own"})
        repo.commit("own")
    if state != "up to date":
        repo.git("checkout", "-q", "main")
        repo.write(**{"x.txt": "x"})
        repo.commit("main moves")
        repo.git("checkout", "-q", "topic")
    repo.write(**{"mywork.txt": "staged", f"{cf.DIR}/2026-11-25-a.md": GOOD["2026-11-25-a.md"] + "\n- mine\n"})
    repo.git("add", "mywork.txt")
    merge_main(repo)  # git itself refuses a real merge with something staged
    assert repo.git("ls-files", "--error-unmatch", "mywork.txt")
    assert repo.git("log", "--all", "--format=%h", "--", "mywork.txt") == ""
    assert repo.git("diff", "--cached", "--name-only") == "mywork.txt"
    assert repo.git("diff", "--name-only") == "changelog.d/2026-11-25-a.md"


def test_a_changelog_resolved_but_not_staged_stops_everything(repo: Repo) -> None:
    lines(repo)
    repo.land()
    repo.write(**{f: changelog(lang, ("\n- main\n", "\n- الرئيسي\n")[lang == "ar"]) for lang, f in cf.FILES.items()})
    repo.commit("main's")
    repo.git("checkout", "-q", "topic/One")
    repo.git("merge", "main", check=False)
    repo.write(**{"CHANGELOG.md": changelog("en", "\n- by hand\n")})
    before = repo.git("rev-parse", "HEAD")
    out = merge_main(repo)
    assert out.returncode == 3 and "CHANGELOG.md has no conflict marker but is not staged" in out.stderr
    code, text = repo.tool("convert", "--merge")
    assert code == 3 and "CHANGELOG.md was resolved by hand" in text
    assert repo.read("CHANGELOG.md") == changelog("en", "\n- by hand\n")
    assert repo.git("rev-parse", "HEAD") == before
    # staged, it stays as it is
    repo.git("add", "CHANGELOG.md")
    assert merge_main(repo).returncode == 0
    assert repo.read("CHANGELOG.md") == changelog("en", "\n- by hand\n")


@pytest.mark.parametrize("merging", [False, True])
@pytest.mark.parametrize("disk", ["untracked", "half added", "edited since"])
def test_a_fragment_only_on_disk_is_no_home_for_an_entry(repo: Repo, disk: str, merging: bool) -> None:
    """The commit after convert, or merge-main.sh's merge, holds every entry once."""
    lines(repo)
    repo.land()
    if merging:
        repo.write(**{f: changelog(lang, ("\n- main\n", "\n- الرئيسي\n")[lang == "ar"]) for lang, f in cf.FILES.items()})
        repo.commit("main's")
    repo.git("checkout", "-q", "topic/One")
    here = {f"{cf.DIR}/2026-11-20-x.md": "### Fixed\n" + LINE["en"], f"{cf.DIR}/2026-11-20-x.ar.md": "### أُصلح\n" + LINE["ar"]}
    if disk == "edited since":
        repo.write(**{f: t.split("\n")[0] + "\n\n- " + ("other", "آخر")[".ar." in f] + "\n" for f, t in here.items()})
        repo.commit("a fragment")
    repo.write(**here)
    if disk == "half added":
        repo.git("add", f"{cf.DIR}/2026-11-20-x.md")
    if merging:
        # a merge refuses to start with something staged: staged once it has
        repo.git("reset", "-q")
        repo.git("merge", "main", check=False)
        if disk == "half added":
            repo.git("add", f"{cf.DIR}/2026-11-20-x.md")
        out = merge_main(repo)
        assert out.returncode == 0 and "moved 1 entry" in out.stdout, out.stdout + out.stderr
    else:
        code, text = repo.tool("convert", "--base", "main")
        assert code == 0 and "moved 1 entry" in text, text
        repo.git("commit", "-qm", "what is staged")
    assert NEW not in repo.git("show", "HEAD:CHANGELOG.md")
    assert repo.git("show", f"HEAD:{cf.DIR}/{datetime.date.today().isoformat()}-topic-one.md").endswith(NEW)
    assert repo.git("show", f"HEAD:{cf.DIR}/{datetime.date.today().isoformat()}-topic-one.ar.md").endswith(LINE["ar"].strip("\n"))
    assert {f: repo.read(f) for f in here} == here  # the files on disk are as they were


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
    code, out = check(repo)
    assert code == 0 and "::" not in out


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
    assert "github.base_ref || inputs.base_ref" in step["env"]["BASE"]
    branch = yaml.safe_load((ROOT / ".github/workflows/ci-branch.yml").read_text(encoding="utf-8"))["jobs"]
    assert branch["pr"]["outputs"]["base_ref"] == "${{ steps.pr.outputs.base_ref }}"
    assert branch["ci"]["with"]["base_ref"] == "${{ needs.pr.outputs.base_ref }}"
    ci = yaml.safe_load((ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8"))["jobs"]
    assert ci["lint"]["with"]["base_ref"] == "${{ inputs.base_ref }}"


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
