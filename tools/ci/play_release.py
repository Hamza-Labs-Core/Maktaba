"""What play-bundles.yml hands Google Play for a release tag (docs/google-play.md).

    play_release.py package APP
        prints APP's package name: the applicationId in its Gradle build file, so the
        package uploaded to is the package the bundle was built as (no second copy of it)

    play_release.py whatsnew TAG MESSAGE_FILE OUT_DIR
        writes OUT_DIR/whatsnew-en-US and OUT_DIR/whatsnew-ar, the release notes Play shows
        testers (r0adkll/upload-google-play's whatsNewDirectory), from the tag's message

The tag's message (git tag -a) is English; a line that is exactly `--- ar ---` starts its
Arabic. A message that is only the tag's name, or a lightweight tag (an empty message), gives
the standard line for that language. Each note is cut to Play's 500 characters.

Standard library only: it runs on the release runner's own python3.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]

# Each Play app and the Gradle build file that names its applicationId.
GRADLE = {
    "player": "apps/android/player/build.gradle.kts",
    "remote": "apps/android/remote/build.gradle.kts",
    "tv": "apps/tv/android/app/build.gradle.kts",
}

# Play's limit on a release note, per language.
MAX_NOTE = 500

AR_MARK = "--- ar ---"

_APP_ID = re.compile(r'^\s*applicationId\s*=\s*"([^"]*)"', re.MULTILINE)
_PACKAGE = re.compile(r"^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z][a-zA-Z0-9_]*)+$")
_TAG = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$")


class PlayReleaseError(Exception):
    """A build file, tag or message play-bundles.yml must not upload with."""


def package_in(gradle_text: str, where: str = "the build file") -> str:
    """The one applicationId in gradle_text."""
    ids = _APP_ID.findall(gradle_text)
    if len(ids) != 1:
        raise PlayReleaseError(f"{where}: want exactly one applicationId, found {len(ids)}")
    pkg = ids[0]
    if not _PACKAGE.match(pkg):
        raise PlayReleaseError(f"{where}: applicationId {pkg!r} is not a package name")
    return pkg


def package(app: str, root: Path = ROOT) -> str:
    """APP's package name, read from its Gradle build file."""
    rel = GRADLE.get(app)
    if rel is None:
        raise PlayReleaseError(f"unknown app {app!r} (want one of {', '.join(sorted(GRADLE))})")
    return package_in((root / rel).read_text(encoding="utf-8"), rel)


def _cut(text: str) -> str:
    """text within Play's limit, cut at a line or a space when it is longer."""
    text = text.strip()
    if len(text) <= MAX_NOTE:
        return text
    head = text[: MAX_NOTE - 1]
    for sep in ("\n", " "):
        at = head.rfind(sep)
        if at >= MAX_NOTE // 2:
            head = head[:at]
            break
    return head.rstrip() + "…"


def whatsnew(tag: str, message: str) -> dict[str, str]:
    """The release notes for tag, by Play locale, from its message."""
    if not _TAG.match(tag):
        raise PlayReleaseError(f"{tag!r} is not a release tag")
    version = tag[1:]
    text = message.replace("\r\n", "\n")
    # git's signature block, if the tag is signed, is no release note
    for sig in ("-----BEGIN PGP SIGNATURE-----", "-----BEGIN SSH SIGNATURE-----"):
        text = text.split(sig, 1)[0]
    lines = text.split("\n")
    mark = next((i for i, line in enumerate(lines) if line.strip() == AR_MARK), len(lines))
    en, ar = "\n".join(lines[:mark]), "\n".join(lines[mark + 1 :])

    def body(part: str) -> str:
        part = part.strip()
        return "" if part in ("", tag, version) else part

    en_note = body(en) or f"Maktaba {version} for testing. What changed is on the {tag} release page on GitHub."
    ar_note = body(ar) or f"مكتبة {version} للاختبار. ما تغيّر في صفحة الإصدار {tag} على GitHub."
    return {"en-US": _cut(en_note), "ar": _cut(ar_note)}


def write_whatsnew(tag: str, message: str, out_dir: Path) -> list[Path]:
    """Writes whatsnew-<locale> for each locale into out_dir."""
    out_dir.mkdir(parents=True, exist_ok=True)
    written = []
    for locale, note in whatsnew(tag, message).items():
        path = out_dir / f"whatsnew-{locale}"
        path.write_text(note + "\n", encoding="utf-8")
        written.append(path)
    return written


def main(argv: list[str]) -> int:
    try:
        if len(argv) == 2 and argv[0] == "package":
            print(package(argv[1]))
            return 0
        if len(argv) == 4 and argv[0] == "whatsnew":
            message = Path(argv[2]).read_text(encoding="utf-8")
            for path in write_whatsnew(argv[1], message, Path(argv[3])):
                print(path)
            return 0
    except (PlayReleaseError, OSError) as e:
        print(f"play_release: {e}", file=sys.stderr)
        return 1
    print(__doc__.split("\n\n", 2)[1], file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
