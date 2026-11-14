#!/usr/bin/env python3
"""The Fire TV APK to the Amazon Appstore (story 58.05), through Amazon's App Submission API.

Two steps, with a person between them:

    amazon_submit.py stage  --apk maktaba-tv-0.3.200-amazon.apk --version 0.3.200
    amazon_submit.py commit --version 0.3.200

`stage` (tv-release.yml, on each tag) signs in with the API's client credentials, opens the app's
edit (or reuses the one already open: Amazon keeps one at a time) and puts the APK in it in place
of the live version's, so the new version waits as a draft in the Developer Console. It never
commits. A committed edit goes to Amazon's review and then the store, and Amazon's API cannot
create or update a Live App Testing version, so a person sends the same APK to Live App Testing
from the console, runs the real-device checklist (docs/stores/amazon.md) on it, and only then:

`commit` (a person or the release session, after the checklist) first runs the checklist guard
(tv_checklist_guard.py --platform amazon): with no passed result for the version it stops with
the reason. Otherwise it commits the open edit, which submits the version to Amazon's review.

The credentials come from the environment, never from the repository: AMAZON_APPSTORE_CLIENT_ID,
AMAZON_APPSTORE_CLIENT_SECRET (a security profile with App Submission API access) and
AMAZON_APPSTORE_APP_ID (the app's id, `amzn1.devportal.mobileapp.…`). Without them it says so and
exits 2. Standard library only.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable, Sequence
from dataclasses import dataclass, field
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import tv_checklist_guard

TOKEN_URL = "https://api.amazon.com/auth/o2/token"
API = "https://developer.amazon.com/api/appstore/v1"
SCOPE = "appstore::apps:readwrite"
NOT_CONFIGURED = 2


@dataclass
class Response:
    status: int
    body: bytes = b""
    headers: dict[str, str] = field(default_factory=dict)

    def json(self) -> object:
        return json.loads(self.body or b"null")


# (method, url, headers, body) -> Response; a test gives its own
Send = Callable[[str, str, dict[str, str], bytes | None], Response]


def urllib_send(method: str, url: str, headers: dict[str, str], body: bytes | None) -> Response:
    req = urllib.request.Request(url, data=body, method=method, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=300) as r:
            return Response(r.status, r.read(), {k.lower(): v for k, v in r.headers.items()})
    except urllib.error.HTTPError as e:
        return Response(e.code, e.read(), {k.lower(): v for k, v in e.headers.items()})


class SubmitError(Exception):
    pass


def _id(r: Response) -> str:
    """The `id` of a JSON object answer ("" for none)."""
    body = r.json()
    return str(body.get("id") or "") if isinstance(body, dict) else ""


class Appstore:
    """The App Submission API calls this release needs, for one app."""

    def __init__(self, app_id: str, client_id: str, secret: str, send: Send = urllib_send) -> None:
        self.app = urllib.parse.quote(app_id, safe="")
        self.client_id = client_id
        self.secret = secret
        self.send = send
        self.token = ""

    def sign_in(self) -> None:
        form = urllib.parse.urlencode(
            {
                "grant_type": "client_credentials",
                "client_id": self.client_id,
                "client_secret": self.secret,
                "scope": SCOPE,
            }
        ).encode()
        r = self.send("POST", TOKEN_URL, {"Content-Type": "application/x-www-form-urlencoded"}, form)
        if r.status != 200:
            raise SubmitError(f"Amazon refused the API credentials ({r.status})")
        body = r.json()
        self.token = str(body.get("access_token", "")) if isinstance(body, dict) else ""
        if not self.token:
            raise SubmitError("Amazon's token answer had no access_token")

    def call(
        self, method: str, path: str, body: bytes | None = None, headers: dict[str, str] | None = None
    ) -> Response:
        h = {"Authorization": f"Bearer {self.token}", "Accept": "application/json", **(headers or {})}
        return self.send(method, f"{API}/applications/{self.app}{path}", h, body)

    def open_edit(self) -> tuple[str, str]:
        """The open edit (id, ETag), made when there is none."""
        r = self.call("GET", "/edits")
        found = _id(r) if r.status == 200 else ""
        if found:
            return found, r.headers.get("etag", "")
        if r.status not in (200, 204, 404):
            raise SubmitError(f"could not read the open edit ({r.status})")
        r = self.call("POST", "/edits")
        made = _id(r) if r.status in (200, 201) else ""
        if not made:
            raise SubmitError(f"could not open an edit ({r.status})")
        return made, r.headers.get("etag", "")

    def stage(self, apk: Path) -> str:
        """Puts [apk] in the open edit in place of the live APK (or as its first). The edit's id."""
        edit, _ = self.open_edit()
        data = apk.read_bytes()
        upload = {"Content-Type": "application/vnd.android.package-archive"}
        r = self.call("GET", f"/edits/{edit}/apks")
        apks = r.json() if r.status == 200 else []
        if isinstance(apks, list) and apks:
            apk_id = str(apks[0]["id"])
            etag = self.call("GET", f"/edits/{edit}/apks/{apk_id}").headers.get("etag", "")
            r = self.call("PUT", f"/edits/{edit}/apks/{apk_id}/replace", data, {**upload, "If-Match": etag})
        else:
            r = self.call("POST", f"/edits/{edit}/apks/upload", data, upload)
        if r.status not in (200, 201):
            raise SubmitError(f"Amazon did not take the APK ({r.status}): {r.body[:300]!r}")
        return edit

    def commit(self) -> str:
        edit, etag = self.open_edit()
        r = self.call("POST", f"/edits/{edit}/commit", None, {"If-Match": etag})
        if r.status not in (200, 201):
            raise SubmitError(f"Amazon did not take the edit ({r.status}): {r.body[:300]!r}")
        return edit


def from_env(env: dict[str, str], send: Send = urllib_send) -> Appstore | None:
    keys = ("AMAZON_APPSTORE_APP_ID", "AMAZON_APPSTORE_CLIENT_ID", "AMAZON_APPSTORE_CLIENT_SECRET")
    if not all(env.get(k) for k in keys):
        return None
    return Appstore(env[keys[0]], env[keys[1]], env[keys[2]], send)


def main(argv: Sequence[str] | None = None, env: dict[str, str] | None = None, send: Send = urllib_send) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = p.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("stage", help="put the APK in the app's open edit (never commits)")
    s.add_argument("--apk", required=True, type=Path)
    s.add_argument("--version", required=True)
    c = sub.add_parser("commit", help="submit the open edit to Amazon's review, once the checklist passed")
    c.add_argument("--version", required=True)
    c.add_argument("--stores", type=Path, default=tv_checklist_guard.STORES, help="the checklists' folder")
    a = p.parse_args(argv)

    if a.cmd == "commit":
        # the real-device checklist first (story 58.16's rule), before any call to Amazon
        if tv_checklist_guard.main(["--platform", "amazon", "--version", a.version, "--stores", str(a.stores)]) != 0:
            print(f"not submitted: the Fire TV checklist has no passed result for app {a.version}", file=sys.stderr)
            return 1

    store = from_env(dict(os.environ) if env is None else env, send)
    if store is None:
        print(
            "AMAZON_APPSTORE_APP_ID, AMAZON_APPSTORE_CLIENT_ID and AMAZON_APPSTORE_CLIENT_SECRET are not set: nothing sent to Amazon"
        )
        return NOT_CONFIGURED
    try:
        store.sign_in()
        if a.cmd == "stage":
            if not a.apk.is_file():
                print(f"{a.apk} is not a file", file=sys.stderr)
                return 1
            edit = store.stage(a.apk)
            print(f"edit={edit}")
            print(
                f"staged app {a.version} in the Appstore's open edit; send it to Live App Testing from the console, run the checklist, then commit"
            )
        else:
            edit = store.commit()
            print(f"edit={edit}")
            print(f"app {a.version} submitted to Amazon's review")
    except SubmitError as e:
        print(f"not submitted: {e}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
