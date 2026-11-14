#!/usr/bin/env python3
"""The review home's scheduled check (Epic 58 story 58.04, T01 and T03).

Every 15 minutes (.github/workflows/review-home-probe.yml) it walks what a store reviewer
walks, against review.maktaba.tv and Maktaba Cloud:

- discovery: the home answers GET /api/discovery/home with its id and API level;
- sign-in: the reviewer's account signs in (a viewer: an admin account fails the check);
- content (T03): every item the reviewer lists carries both titles (English and Arabic) and
  its licence note, and every item of the manifest is there;
- streams: one stream session per test file (4K HEVC HDR10, 1080p H.264, the MKV with EAC3
  and WebVTT, the file that needs a transcode);
- cloud code: a code from Maktaba Cloud (POST /v1/tv/codes), approved on the home as the
  reviewer (POST /api/pairing/cloud-approve), polled until the cloud hands over the grant,
  and redeemed on the first of the home's connections that answers, exactly as a TV does
  (story 58.03). The test client then signs itself out, so the reviewer's devices do not
  pile up.

Each check's result goes to the witness's Pushgateway (review_home_probe_success{check=...},
job review_home) when PUSHGATEWAY_URL is set, where review_home_down alerts; a failed check
fails the run, and GitHub mails the owner.

Env: REVIEW_HOME_URL (https://review.maktaba.tv), CLOUD_URL (https://api.maktaba.tv),
REVIEW_HOME_VIEWER (reviewer), REVIEW_HOME_VIEWER_PASSWORD, PUSHGATEWAY_URL, PUSHGATEWAY_TOKEN.
Standard library only.
"""

from __future__ import annotations

import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

sys.path.insert(0, str(Path(__file__).resolve().parent))
import content  # noqa: E402

CLIENT = "review-home-probe/1.0 (tv)"
CHECKS = ("discovery", "signin", "content", "streams", "cloud_code")


class ProbeError(Exception):
    pass


@dataclass
class Http:
    """A small JSON client; tests point it at a local server."""

    timeout: float = 20.0

    def call(self, method: str, url: str, body: Any = None, token: str = "") -> tuple[int, Any]:
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Accept", "application/json")
        req.add_header("X-Maktaba-Client", CLIENT)
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if token:
            req.add_header("Authorization", f"Bearer {token}")
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as r:  # noqa: S310 - the probe's own https URLs
                raw = r.read()
                status = r.status
        except urllib.error.HTTPError as e:
            raw, status = e.read(), e.code
        except (urllib.error.URLError, TimeoutError, OSError) as e:
            raise ProbeError(f"{method} {url}: {e}") from e
        try:
            return status, json.loads(raw) if raw else None
        except ValueError:
            return status, None


@dataclass
class Probe:
    home: str
    cloud: str
    viewer: str
    password: str
    manifest: dict[str, Any]
    http: Http = field(default_factory=Http)
    sleep: Callable[[float], None] = time.sleep
    poll_for: float = 90.0
    token: str = ""
    videos: list[dict[str, Any]] = field(default_factory=list)

    def url(self, base: str, path: str) -> str:
        return base.rstrip("/") + path

    def discovery(self) -> str:
        st, body = self.http.call("GET", self.url(self.home, "/api/discovery/home"))
        if st != 200 or not isinstance(body, dict) or not body.get("id") or "api_level" not in body:
            raise ProbeError(f"discovery answered {st}")
        return f"home {body.get('name', '?')}, API level {body['api_level']}"

    def signin(self) -> str:
        st, body = self.http.call(
            "POST", self.url(self.home, "/api/auth/login"), {"username": self.viewer, "password": self.password}
        )
        if st != 200 or not isinstance(body, dict) or not body.get("access_token"):
            raise ProbeError(f"sign-in answered {st}")
        user = body.get("user") or {}
        if user.get("is_admin"):
            raise ProbeError("the reviewer's account is an admin; it must be a viewer")
        self.token = body["access_token"]
        return f"signed in as {self.viewer}"

    def list_videos(self) -> list[dict[str, Any]]:
        out: list[dict[str, Any]] = []
        path = "/api/videos?limit=200"
        for _ in range(20):
            st, body = self.http.call("GET", self.url(self.home, path), token=self.token)
            if st != 200 or not isinstance(body, dict):
                raise ProbeError(f"the video list answered {st}")
            out += body.get("items") or []
            nxt = body.get("next")
            if not nxt:
                break
            path = f"/api/videos?limit=200&cursor={urllib.parse.quote(str(nxt))}"
        return out

    def content(self) -> str:
        self.videos = self.list_videos()
        problems = content_problems(self.videos, self.manifest)
        if problems:
            raise ProbeError("; ".join(problems))
        return f"{len(self.videos)} items, each with both titles and a licence note"

    def streams(self) -> str:
        by_title = {v.get("title"): v for v in self.videos}
        done = []
        for it in self.manifest["items"]:
            for cover in it.get("covers", []):
                v = by_title.get(content_title(it))
                if v is None:
                    raise ProbeError(f"{cover}: {it['id']} is not listed")
                st, body = self.http.call(
                    "POST", self.url(self.home, "/api/stream/sessions"), {"video_id": v["id"], "format": "hls"}, self.token
                )
                if st != 200 or not isinstance(body, dict) or not body.get("session_id"):
                    raise ProbeError(f"{cover}: the stream session answered {st}")
                self.http.call("DELETE", self.url(self.home, f"/api/stream/sessions/{body['session_id']}"), token=self.token)
                done.append(f"{cover} ({body.get('mode', '?')})")
        if len(done) < len(content.COVERS):
            raise ProbeError(f"only {len(done)} formats have a test file")
        return ", ".join(done)

    def cloud_code(self) -> str:
        st, code = self.http.call(
            "POST", self.url(self.cloud, "/v1/tv/codes"), {"platform": "probe", "app_version": "review-home-probe"}
        )
        if st != 201 or not isinstance(code, dict) or not code.get("code"):
            raise ProbeError(f"the cloud's code answered {st}")
        st, _ = self.http.call(
            "POST", self.url(self.home, "/api/pairing/cloud-approve"), {"code": code["code"]}, self.token
        )
        if st != 200:
            raise ProbeError(f"cloud-approve answered {st}")
        interval = float(code.get("interval") or 5)
        waited = 0.0
        while True:
            st, body = self.http.call(
                "POST", self.url(self.cloud, "/v1/tv/codes/poll"), {"code": code["code"], "poll_secret": code["poll_secret"]}
            )
            if st == 200 and isinstance(body, dict) and body.get("grant"):
                break
            if st not in (202, 429) or waited >= self.poll_for:
                raise ProbeError(f"the cloud's poll answered {st} after {waited:.0f} s")
            self.sleep(interval)
            waited += interval
        tried = []
        for c in body.get("connections") or []:
            try:
                st, tok = self.http.call(
                    "POST", self.url(c["url"], "/api/pairing/device/token"), {"device_code": body["grant"]}
                )
            except ProbeError:
                tried.append(f"{c.get('kind')}: unreachable")
                continue
            if st == 200 and isinstance(tok, dict) and tok.get("access_token"):
                self.http.call("POST", self.url(c["url"], "/api/auth/logout"), token=tok["access_token"])
                return f"signed in through {c.get('kind')} ({', '.join(tried) or 'first try'})"
            tried.append(f"{c.get('kind')}: {st}")
        raise ProbeError(f"no connection redeemed the grant ({', '.join(tried) or 'none listed'})")

    def run(self) -> dict[str, tuple[bool, str]]:
        results: dict[str, tuple[bool, str]] = {}
        steps = [("discovery", self.discovery), ("signin", self.signin)]
        for name, fn in steps:
            results[name] = _attempt(fn)
        if results["signin"][0]:
            results["content"] = _attempt(self.content)
            results["streams"] = _attempt(self.streams) if self.videos else (False, "no videos to play")
            results["cloud_code"] = _attempt(self.cloud_code)
            self.http.call("POST", self.url(self.home, "/api/auth/logout"), token=self.token)
        for name in CHECKS:
            results.setdefault(name, (False, "not run: the sign-in failed"))
        return results


def _attempt(fn: Callable[[], str]) -> tuple[bool, str]:
    try:
        return True, fn()
    except ProbeError as e:
        return False, str(e)


def content_title(it: dict[str, Any]) -> str:
    """The title the seeding tool writes (api/internal/reviewhome Both)."""
    return f"{it['title_en']} · {it['title_ar']}"


def content_problems(videos: list[dict[str, Any]], manifest: dict[str, Any]) -> list[str]:
    """T03: every listed item has both titles and a licence note, and every manifest item is listed."""
    problems = []
    for v in videos:
        title, desc = str(v.get("title") or ""), str(v.get("description") or "")
        if not (content.has_latin(title) and content.has_arabic(title)):
            problems.append(f"{title or v.get('id')}: needs an English and an Arabic title")
        if "Licence:" not in desc or "الترخيص:" not in desc:
            problems.append(f"{title or v.get('id')}: no licence note")
    titles = {str(v.get("title") or "") for v in videos}
    missing = [it["id"] for it in manifest["items"] if content_title(it) not in titles]
    if missing:
        problems.append(f"not listed: {', '.join(missing)}")
    return problems


def metrics(results: dict[str, tuple[bool, str]], now: float) -> str:
    lines = [f'review_home_probe_success{{check="{k}"}} {1 if ok else 0}' for k, (ok, _) in results.items()]
    lines.append(f"review_home_probe_last_run_seconds {int(now)}")
    return "\n".join(lines) + "\n"


def push(text: str, url: str, token: str) -> None:
    req = urllib.request.Request(
        url.rstrip("/") + "/metrics/job/review_home/instance/actions", data=text.encode(), method="PUT"
    )
    if token:
        req.add_header("Authorization", f"Bearer {token}")
    try:
        urllib.request.urlopen(req, timeout=10).close()  # noqa: S310 - the witness's Pushgateway
    except (urllib.error.URLError, OSError) as e:
        print(f"probe: the Pushgateway did not take the results: {e}", file=sys.stderr)


def main() -> int:
    password = os.environ.get("REVIEW_HOME_VIEWER_PASSWORD", "")
    if not password:
        print("probe: REVIEW_HOME_VIEWER_PASSWORD is not set (the release secret)", file=sys.stderr)
        return 2
    p = Probe(
        home=os.environ.get("REVIEW_HOME_URL", "https://review.maktaba.tv"),
        cloud=os.environ.get("CLOUD_URL", "https://api.maktaba.tv"),
        viewer=os.environ.get("REVIEW_HOME_VIEWER", "reviewer"),
        password=password,
        manifest=content.load(),
    )
    results = p.run()
    for name, (ok, note) in results.items():
        print(f"probe: {name}: {'ok' if ok else 'FAILED'}: {note}")
    if os.environ.get("PUSHGATEWAY_URL"):
        push(metrics(results, time.time()), os.environ["PUSHGATEWAY_URL"], os.environ.get("PUSHGATEWAY_TOKEN", ""))
    failed = [k for k, (ok, _) in results.items() if not ok]
    if failed:
        print(f"::error::the review home failed: {', '.join(failed)}")
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
