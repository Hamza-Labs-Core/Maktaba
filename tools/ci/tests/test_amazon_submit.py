"""Story 58.05: the Fire TV APK goes to the Amazon Appstore as a draft (stage) and is submitted
to Amazon's review only after the real-device checklist passed for its version (commit)."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(ROOT / "tools" / "release"))

import amazon_submit as sub

ENV = {
    "AMAZON_APPSTORE_APP_ID": "amzn1.devportal.mobileapp.abc",
    "AMAZON_APPSTORE_CLIENT_ID": "client",
    "AMAZON_APPSTORE_CLIENT_SECRET": "secret",
}
APP = "/applications/amzn1.devportal.mobileapp.abc"


class FakeAmazon:
    """Amazon's token and App Submission API, as far as these calls go."""

    def __init__(self, open_edit: bool = True, apks: bool = True, token_status: int = 200) -> None:
        self.calls: list[tuple[str, str, dict[str, str], bytes | None]] = []
        self.open_edit = open_edit
        self.apks = apks
        self.token_status = token_status

    def __call__(self, method: str, url: str, headers: dict[str, str], body: bytes | None) -> sub.Response:
        self.calls.append((method, url, headers, body))
        if url == sub.TOKEN_URL:
            return sub.Response(self.token_status, json.dumps({"access_token": "tok"}).encode())
        path = url.removeprefix(sub.API)
        assert path.startswith(APP), path
        assert headers["Authorization"] == "Bearer tok"
        rest = path.removeprefix(APP)
        if (method, rest) == ("GET", "/edits"):
            if self.open_edit:
                return sub.Response(200, b'{"id":"e1","status":"IN_PROGRESS"}', {"etag": "edit-tag"})
            return sub.Response(200, b"{}")
        if (method, rest) == ("POST", "/edits"):
            return sub.Response(200, b'{"id":"e2"}', {"etag": "new-tag"})
        if method == "GET" and rest.endswith("/apks"):
            return sub.Response(200, b'[{"id":"apk1","versionCode":1}]' if self.apks else b"[]")
        if method == "GET" and rest.endswith("/apks/apk1"):
            return sub.Response(200, b'{"id":"apk1"}', {"etag": "apk-tag"})
        if method == "PUT" and rest.endswith("/apks/apk1/replace"):
            return sub.Response(200, b'{"id":"apk1"}')
        if method == "POST" and rest.endswith("/apks/upload"):
            return sub.Response(200, b'{"id":"apk2"}')
        if method == "POST" and rest.endswith("/commit"):
            return sub.Response(200, b'{"id":"e1","status":"SUBMITTED"}')
        return sub.Response(404, b"{}")

    def paths(self) -> list[str]:
        return [f"{m} {u.removeprefix(sub.API).removeprefix(APP)}" for m, u, _, _ in self.calls if u != sub.TOKEN_URL]


@pytest.fixture
def apk(tmp_path: Path) -> Path:
    p = tmp_path / "maktaba-tv-0.3.200-amazon.apk"
    p.write_bytes(b"PK\x03\x04 not really an apk")
    return p


def test_stage_replaces_the_live_apk_in_the_open_edit_and_never_commits(apk: Path) -> None:
    amazon = FakeAmazon()
    assert sub.main(["stage", "--apk", str(apk), "--version", "0.3.200"], env=ENV, send=amazon) == 0
    assert amazon.paths() == [
        "GET /edits",
        "GET /edits/e1/apks",
        "GET /edits/e1/apks/apk1",
        "PUT /edits/e1/apks/apk1/replace",
    ]
    method, _, headers, body = amazon.calls[-1]
    assert headers["If-Match"] == "apk-tag"
    assert headers["Content-Type"] == "application/vnd.android.package-archive"
    assert body == apk.read_bytes()
    assert not any("commit" in p for p in amazon.paths())
    # the client credentials grant, with the API's scope
    token = amazon.calls[0][3] or b""
    assert b"grant_type=client_credentials" in token and b"scope=appstore%3A%3Aapps%3Areadwrite" in token


def test_stage_opens_an_edit_and_uploads_the_first_apk(apk: Path) -> None:
    amazon = FakeAmazon(open_edit=False, apks=False)
    assert sub.main(["stage", "--apk", str(apk), "--version", "0.3.200"], env=ENV, send=amazon) == 0
    assert amazon.paths() == ["GET /edits", "POST /edits", "GET /edits/e2/apks", "POST /edits/e2/apks/upload"]


def test_without_credentials_nothing_is_sent(apk: Path, capsys: pytest.CaptureFixture[str]) -> None:
    amazon = FakeAmazon()
    assert sub.main(["stage", "--apk", str(apk), "--version", "0.3.200"], env={}, send=amazon) == sub.NOT_CONFIGURED
    assert amazon.calls == []
    assert "not set" in capsys.readouterr().out


def test_refused_credentials_stop_the_stage(apk: Path) -> None:
    amazon = FakeAmazon(token_status=401)
    assert sub.main(["stage", "--apk", str(apk), "--version", "0.3.200"], env=ENV, send=amazon) == 1
    assert amazon.paths() == []


def _page(tmp_path: Path, results: str) -> Path:
    stores = tmp_path / "stores"
    stores.mkdir()
    (stores / "amazon.md").write_text(f"# Fire TV\n\n## Results\n\n{results}\n", encoding="utf-8")
    return stores


def test_commit_needs_a_passed_checklist_for_the_version(tmp_path: Path) -> None:
    stores = _page(tmp_path, "- Fire TV checklist failed on Fire TV Stick 4K Max, app 0.3.200 (2026-12-01): DV")
    amazon = FakeAmazon()
    assert sub.main(["commit", "--version", "0.3.200", "--stores", str(stores)], env=ENV, send=amazon) == 1
    assert amazon.calls == [], "nothing goes to Amazon without a passed checklist"


def test_commit_submits_the_open_edit_once_the_checklist_passed(tmp_path: Path) -> None:
    stores = _page(
        tmp_path, "- Fire TV checklist passed on Fire TV Stick 4K Max/Fire TV Stick (3rd gen), app 0.3.200 (2026-12-02)"
    )
    amazon = FakeAmazon()
    assert sub.main(["commit", "--version", "0.3.200", "--stores", str(stores)], env=ENV, send=amazon) == 0
    assert amazon.paths() == ["GET /edits", "POST /edits/e1/commit"]
    assert amazon.calls[-1][2]["If-Match"] == "edit-tag"
