#!/usr/bin/env python3
"""Maktaba Cloud, one node: the end-to-end test a deploy runs (docs/cloud-single-node.md, "Tests").

Run by .github/workflows/cloud-deploy.yml on its runner, against the node it just deployed:

    python3 deploy/cloud-relay/tests/e2e.py --mode full    # QA: everything below
    python3 deploy/cloud-relay/tests/e2e.py --mode smoke   # production: no restart, no load

What it proves, each a check in the summary:

  tls        each public name (relay apex, a home's name, api, app) answers HTTPS with a
             certificate for it, from a public CA (or the node's own CA before DNS exists)
  health     /healthz and /readyz on the relay and the api; the cloud web app's page
  account    a new cloud account: registered, confirmed and given the Basic plan by the
             node's operator tool (ops/cloud-ops.py), signed in
  home       a real home (the release's maktaba-server image, on this runner) with a test
             video: set up headlessly, its claim code minted by the account, linked through
             the relay, online in the account's homes
  licence    the home holds the cloud's licence for its owner's plan (signed with the key its
             release embeds), so it serves requests that come through the relay
  link       the home's admin linked to the cloud account (the link flow the home's
             Linked accounts page runs, with the cloud web app's step done by API)
  relay-play the home's own sign-in through its relay name, and the video played through the
             relay: a stream session, its bytes, checked against the file
  cloud-play sign in through the cloud (a sign-in assertion, POST /api/auth/cloud through the
             home's relay name) and play again: needs the home to trust the cloud's licence
             (the cloud signs with the key releases embed, docs/licensing.md)
  plans      the Free plan has no relay (402 relay_needs_plan); Basic does again
  restart    (full) the relay container restarted: the home is back through the relay, plays
  ended      what the test registered is ended, whatever failed before: no test account left
             active, no home linked (the cloud's 30-day deletion hold remains)
  broadcast-off  broadcasting is off: every public broadcast and moderation route answers 404
             (with and without a signed-in account), the providers and plans lists offer
             none, no home is a test publisher, and only the node's six services run
  firewall   from outside, only 22, 80 and 443 answer on TCP; UFW denies the rest
  backup     the nightly backup's timer is on; a backup taken now restores into a scratch
             database with the live accounts and homes
  load       (full) N players through the relay for a minute while the node's CPU, memory
             and network are sampled: the sizing figures (sizing.json)

Nothing secret is printed: the account's password, its tokens, the claim code and assertions
are made here and masked in the CI log (::add-mask::) before any use.

Env: RELAY_DOMAIN, API_DOMAIN, APP_DOMAIN, NODE_IP, HOME_IMAGE, NODE_SSH (a command that runs
its arguments on the node, stdin passed through), RUN_ID, DEPLOY_ID (the deploy whose lease to renew), OUT (the results folder), CA_FILE
(optional: the node's own CA, RELAY_TLS=internal), PIN_HOSTS=1 (names not in public DNS yet:
the runner's /etc/hosts points them at NODE_IP), LOAD_PLAYERS (8), LOAD_SECONDS (60).
"""
from __future__ import annotations

import argparse
import concurrent.futures as cf
import hashlib
import json
import os
import secrets
import shlex
import socket
import ssl
import subprocess
import sys
import threading
import time
import tomllib
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any, Callable
from urllib.parse import urljoin, urlsplit

RELAY = os.environ["RELAY_DOMAIN"]
API_HOST = os.environ["API_DOMAIN"]
APP_HOST = os.environ["APP_DOMAIN"]
NODE_IP = os.environ["NODE_IP"]
HOME_IMAGE = os.environ["HOME_IMAGE"]
NODE_SSH = shlex.split(os.environ["NODE_SSH"])
RUN_ID = os.environ.get("RUN_ID", str(int(time.time())))
OUT = Path(os.environ.get("OUT", "e2e-out"))
CA_FILE = os.environ.get("CA_FILE", "")
API = f"https://{API_HOST}"
SLUG = f"e2e-{RUN_ID}"[:30].rstrip("-")
HOME = f"https://{SLUG}.{RELAY}"
CONTAINER = f"maktaba-e2e-{RUN_ID}"
VOL_DATA, VOL_MEDIA = f"{CONTAINER}-data", f"{CONTAINER}-media"
VOLUME = "/var/lib/maktaba"
CONFIG = f"{VOLUME}/config/maktaba/server.toml"
MEDIA = "/media/films"

results: list[dict[str, Any]] = []


def log(msg: str) -> None:
    print(f"[e2e {time.strftime('%H:%M:%S')}] {msg}", flush=True)


def mask(value: str) -> str:
    print(f"::add-mask::{value}", flush=True)
    return value


def check(name: str, fn: Callable[[], str], needs: tuple[bool, ...] = ()) -> bool:
    """Runs one check and records it; a check whose prerequisite failed is recorded as skipped
    (and counts as a failure of the run, through its prerequisite)."""
    if not all(needs):
        results.append({"check": name, "ok": None, "detail": "skipped: a check it needs failed", "sec": 0})
        log(f"skip {name}")
        return False
    t0 = time.monotonic()
    log(f"-- {name}")
    try:
        detail = fn()
        results.append({"check": name, "ok": True, "detail": detail, "sec": round(time.monotonic() - t0, 1)})
        log(f"ok  {name}: {detail}")
        return True
    except Exception as e:  # noqa: BLE001 - every check reports; the run fails at the end
        results.append({"check": name, "ok": False, "detail": str(e)[:1500], "sec": round(time.monotonic() - t0, 1)})
        log(f"FAIL {name}: {str(e)[:1500]}")
        return False


def wait(what: str, probe: Callable[[], Any], seconds: float, every: float = 5) -> Any:
    deadline = time.monotonic() + seconds
    last: Any = None
    while True:
        try:
            got = probe()
            if got:
                return got
        except Exception as e:  # noqa: BLE001 - a probe that failed is tried again
            last = e
        if time.monotonic() > deadline:
            raise AssertionError(f"{what}: not after {int(seconds)} s" + (f" (last: {str(last)[:300]})" if last else ""))
        time.sleep(every)


# --- HTTP ---------------------------------------------------------------------------------

def tls_context() -> ssl.SSLContext:
    """The public CAs (or the node's own, CA_FILE), TLS 1.2 or newer."""
    ctx = ssl.create_default_context(cafile=CA_FILE or None)
    ctx.minimum_version = ssl.TLSVersion.TLSv1_2
    return ctx


CTX = tls_context()


def http(method: str, url: str, body: Any = None, token: str = "", raw: bool = False,
         timeout: float = 30, headers: dict[str, str] | None = None) -> tuple[int, Any]:
    h = {"Content-Type": "application/json", "X-Maktaba-Client": "native"}
    if token:
        h["Authorization"] = "Bearer " + token
    h.update(headers or {})
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=CTX) as r:
            b = r.read()
            return r.status, b if raw else _json(b)
    except urllib.error.HTTPError as e:
        b = e.read()
        return e.code, b if raw else _json(b)


def _json(b: bytes) -> Any:
    try:
        return json.loads(b) if b else None
    except ValueError:
        return b[:300].decode("utf-8", "replace")


def need(status: int, body: Any, ok: tuple[int, ...], what: str) -> Any:
    if status not in ok:
        err = body.get("error") if isinstance(body, dict) else str(body)[:200]
        raise AssertionError(f"{what}: {status} {err}")
    return body


# --- the node -------------------------------------------------------------------------------

def node(cmd: str, stdin: str | None = None, check_rc: bool = True, timeout: float = 600) -> str:
    p = subprocess.run([*NODE_SSH, cmd], input=stdin, capture_output=True, text=True, timeout=timeout)
    if check_rc and p.returncode != 0:
        raise AssertionError(f"on the node, `{cmd.split()[0]}...` exited {p.returncode}: {p.stderr.strip()[-400:]}")
    return p.stdout


def ops(args: str, email: str) -> str:
    return node(f"/opt/maktaba-relay/ops/cloud-ops.py {args}", stdin=email + "\n").strip()


def keep_renewing(stop: threading.Event) -> None:
    """The deploy's lease on the node, renewed while this runs (ops/rollout.sh, "A lost runner"):
    a runner that goes away stops renewing, and the node goes back by itself."""
    deploy = os.environ.get("DEPLOY_ID", "")
    while deploy and not stop.wait(60):
        try:
            node(f"/usr/local/sbin/maktaba-rollout renew {shlex.quote(deploy)}", timeout=40)
        except Exception as e:  # noqa: BLE001 - a missed renewal is one of many
            log(f"renew: {str(e)[:200]}")


def docker(*args: str, stdin: str | None = None, check_rc: bool = True, timeout: float = 900) -> subprocess.CompletedProcess:
    p = subprocess.run(["docker", *args], input=stdin, capture_output=True, text=True, timeout=timeout)
    if check_rc and p.returncode != 0:
        raise AssertionError(f"docker {args[0]} exited {p.returncode}: {(p.stderr or p.stdout).strip()[-600:]}")
    return p


# --- the checks -----------------------------------------------------------------------------

def pin_hosts() -> None:
    names = [RELAY, API_HOST, APP_HOST, f"{SLUG}.{RELAY}", f"tls-probe.{RELAY}"]
    lines = "".join(f"{NODE_IP} {n}\n" for n in names)
    subprocess.run(["sudo", "tee", "-a", "/etc/hosts"], input=lines, text=True, check=True, capture_output=True)
    log(f"/etc/hosts: {len(names)} names -> {NODE_IP} (not in public DNS yet)")


def tls_one(host: str) -> str:
    with socket.create_connection((host, 443), timeout=15) as s:
        with tls_context().wrap_socket(s, server_hostname=host) as t:
            c = t.getpeercert()
    issuer = dict(x[0] for x in c["issuer"]).get("organizationName") or dict(x[0] for x in c["issuer"]).get("commonName")
    days = int((ssl.cert_time_to_seconds(c["notAfter"]) - time.time()) / 86400)
    if days < 7:
        raise AssertionError(f"{host}: the certificate ends in {days} days")
    return f"{host} ({issuer}, {days} d)"


def check_tls() -> str:
    # a home's name: the wildcard covers it (dns); on-demand only for a known slug (http), so
    # the home's name is checked once the test home exists (play); here the apex, api and app.
    # A first deploy's certificates come a minute or two after Caddy starts.
    return "; ".join(wait(f"a certificate for {h}", lambda h=h: tls_one(h), 6 * 60, every=10)
                     for h in (RELAY, API_HOST, APP_HOST))


def check_health() -> str:
    out = []
    for base in (f"https://{RELAY}", API):
        for path in ("/healthz", "/readyz"):
            s, b = http("GET", base + path)
            need(s, b, (200,), f"{base}{path}")
            out.append(f"{base.split('//')[1]}{path} 200")
    s, b = http("GET", f"https://{APP_HOST}/", raw=True)
    need(s, b, (200,), "the cloud web app")
    if b"<html" not in b.lower() and b"<!doctype" not in b.lower():
        raise AssertionError("the cloud web app's page is not HTML")
    out.append(f"{APP_HOST}/ 200")
    return ", ".join(out)


class Account:
    def __init__(self) -> None:
        self.email = f"ci-e2e-{RUN_ID}@example.com"
        self.password = mask(secrets.token_urlsafe(24))
        self.token = ""

    def login(self) -> str:
        s, b = http("POST", f"{API}/v1/auth/login", {"email": self.email, "password": self.password})
        need(s, b, (200,), "cloud sign-in")
        self.token = mask(b["access_token"])
        return self.token


def check_account(acct: Account) -> str:
    s, b = http("POST", f"{API}/v1/auth/register", {"email": acct.email, "password": acct.password,
                                                     "display_name": "CI e2e"})
    need(s, b, (200, 201, 202), "register")
    s, b = http("POST", f"{API}/v1/auth/login", {"email": acct.email, "password": acct.password})
    if s != 403 or not isinstance(b, dict) or b.get("error") != "email_not_verified":
        raise AssertionError(f"sign-in before confirming: {s} {b} (want 403 email_not_verified)")
    if ops("verify-email", acct.email) not in ("confirmed", "already confirmed"):
        raise AssertionError("cloud-ops verify-email")
    if ops("set-plan basic", acct.email) != "plan basic":
        raise AssertionError("cloud-ops set-plan basic")
    acct.login()
    s, b = http("GET", f"{API}/v1/account/me", token=acct.token)
    need(s, b, (200,), "account/me")
    return f"registered; 403 email_not_verified before; confirmed and Basic by cloud-ops; signed in (plan {b.get('plan', '?')})"


# The public routes of broadcasting and its moderation (cloud/internal/handlers: broadcast,
# moderation, billing/broadcast.go), and the operator's, which no public name serves. With
# broadcasting off each answers 404: not 401, which would say the route is there.
ZERO = "00000000-0000-0000-0000-000000000000"
BROADCAST_ROUTES = (
    ("POST", "/v1/broadcast/channels"), ("DELETE", f"/v1/broadcast/channels/{ZERO}"),
    ("GET", "/v1/broadcast/state"), ("POST", "/v1/broadcast/ingest-token"),
    ("GET", "/v1/broadcast/agreement"), ("POST", "/v1/broadcast/agreement"),
    ("GET", "/v1/broadcast/analytics"),
    ("GET", "/v1/broadcast/takedowns"), ("POST", f"/v1/broadcast/takedowns/{ZERO}/ack"),
    ("GET", "/v1/broadcast/moderation"), ("POST", "/v1/broadcast/moderation/counter-notices"),
    ("GET", "/v1/broadcast/revocations"), ("GET", "/v1/broadcast/access"), ("PUT", "/v1/broadcast/viewer-limits"),
    ("GET", "/v1/broadcast/invites"), ("POST", "/v1/broadcast/invites"), ("DELETE", f"/v1/broadcast/invites/{ZERO}"),
    ("GET", f"/v1/broadcast/homes/{ZERO}/access"), ("GET", "/v1/broadcast/home/analytics"),
    ("POST", "/_edge/usage"), ("GET", f"/_edge/publishers/{ZERO}"),
    ("POST", "/v1/moderation/notices"), ("POST", "/v1/moderation/reports"),
    ("GET", "/v1/billing/broadcast"), ("PUT", "/v1/billing/broadcast"),
    ("PATCH", "/v1/billing/broadcast/payg"), ("PATCH", "/v1/billing/broadcast/extras"),
    ("GET", "/v1/admin/moderation"), ("GET", "/v1/admin/broadcast/geo-deny"),
    ("GET", "/v1/admin/broadcast/analytics"), ("GET", "/v1/admin/broadcast/revocations"),
    ("POST", f"/v1/admin/servers/{ZERO}/broadcast"),
)
SERVICES = {"caddy", "maktaba-cloud", "api", "worker", "postgres", "redis"}


def check_broadcast_off(acct: Account) -> str:
    wrong = []
    for who, token in (("no account", ""), ("a signed-in account", acct.token)):
        for method, path in BROADCAST_ROUTES:
            s, _ = http(method, API + path, None if method in ("GET", "DELETE") else {}, token)
            if s != 404:
                wrong.append(f"{method} {path} ({who}) {s}")
    # the app's name proxies /v1 to the same api: one of each family there
    for method, path in (("GET", "/v1/broadcast/state"), ("POST", "/v1/moderation/reports")):
        s, _ = http(method, f"https://{APP_HOST}{path}", None if method == "GET" else {})
        if s != 404:
            wrong.append(f"{method} {APP_HOST}{path} {s}")
    s, b = http("GET", API + "/v1/auth/providers")
    if not isinstance(b, dict) or b.get("broadcast") is not False:
        wrong.append("auth/providers does not say broadcast false")
    s, b = http("GET", API + "/v1/billing/plans")
    if not isinstance(b, dict) or b.get("broadcast"):
        wrong.append("billing/plans offers broadcasting")
    compose = "cd /opt/maktaba-relay && docker compose --env-file .env "
    n = node(compose + "exec -T postgres sh -c 'psql -X -A -t -U \"$POSTGRES_USER\" -d \"$POSTGRES_DB\" "
             "-c \"SELECT count(*) FROM servers WHERE broadcast_test_publisher\"' </dev/null").strip()
    if n != "0":
        wrong.append(f"{n} homes are test publishers")
    extra = set(node("docker ps --format '{{.Label \"com.docker.compose.service\"}}|{{.Names}}'").split())
    extra = {x for x in extra if x.split("|")[0] not in SERVICES}
    if extra:
        wrong.append(f"containers besides the node's services: {sorted(extra)}")
    conf = tomllib.loads(node("cat /opt/maktaba-relay/cloud.toml"))
    if conf.get("broadcast", {}).get("enabled") is not False:
        wrong.append("cloud.toml does not set broadcast.enabled = false")
    if wrong:
        raise AssertionError(f"{len(wrong)} wrong: " + "; ".join(wrong))
    return (f"{len(BROADCAST_ROUTES)} routes answer 404 with and without an account; no offer in the providers "
            "or plans; no test publisher; only the node's services run")


class Home:
    def __init__(self) -> None:
        self.password = mask(secrets.token_urlsafe(18))
        self.token = ""
        self.local = "http://127.0.0.1:8088"
        self.server_id = ""
        self.video: dict[str, Any] = {}
        self.sha256 = ""

    def call(self, method: str, path: str, body: Any = None, raw: bool = False, base: str = "", token: str = "",
             timeout: float = 30) -> tuple[int, Any]:
        url = path if path.startswith("http") else (base or self.local) + path
        return http(method, url, body, token or self.token, raw=raw, timeout=timeout)

    def login(self) -> None:
        s, b = http("POST", self.local + "/api/auth/login", {"username": "e2e-admin", "password": self.password})
        need(s, b, (200,), "home sign-in")
        self.token = mask(b["access_token"])


def make_home(acct: Account, home: Home) -> str:
    for v in (VOL_DATA, VOL_MEDIA):
        docker("volume", "create", v)
    # the test video: 40 s of 1080p H.264 at ~8 Mbit/s with sound, made by the image's ffmpeg
    docker("run", "--rm", "--user", "0:0", "--entrypoint", "sh", "-v", f"{VOL_MEDIA}:{MEDIA}", HOME_IMAGE, "-c",
           f"ffmpeg -loglevel error -f lavfi -i testsrc2=size=1920x1080:rate=25 -f lavfi -i sine=frequency=440"
           f" -t 40 -c:v libx264 -preset veryfast -b:v 8M -pix_fmt yuv420p -c:a aac -movflags +faststart"
           f" {MEDIA}/e2e-test-pattern.mp4 && chown -R 1000:1000 {MEDIA}")
    home.sha256 = docker("run", "--rm", "--entrypoint", "sha256sum", "-v", f"{VOL_MEDIA}:{MEDIA}:ro", HOME_IMAGE,
                         f"{MEDIA}/e2e-test-pattern.mp4").stdout.split()[0]
    # server.toml before setup (tests/nas/cell.py): small, offline, no quiet hours
    docker("run", "--rm", "-i", "--entrypoint", "sh", "-v", f"{VOL_DATA}:{VOLUME}", HOME_IMAGE, "-c",
           f"umask 077 && mkdir -p {VOLUME}/config/maktaba && cat > {CONFIG}",
           stdin='[llm]\nenabled = "off"\n\n[transcription]\nmodel = "Systran/faster-whisper-tiny"\nrefine = "off"\n\n'
                 '[schedule]\nquiet_hours = ""\n')
    answers = {"admin": {"username": "e2e-admin", "password": home.password},
               "libraries": {"roots": [MEDIA], "path_map": []},
               "models": {"source": "huggingface", "hf_token": ""}, "plex": {"action": "skip"}}
    t0 = time.monotonic()
    p = docker("run", "--rm", "-i", "--network", "host", "-v", f"{VOL_DATA}:{VOLUME}", "-v", f"{VOL_MEDIA}:{MEDIA}:ro",
               HOME_IMAGE, "setup", "--answers", "/dev/stdin", "--lang", "en", "--bundle", "core",
               stdin=json.dumps(answers), check_rc=False, timeout=45 * 60)
    (OUT / "home-setup.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
    if p.returncode != 0:
        raise AssertionError(f"home setup exited {p.returncode} (home-setup.log)")
    setup_sec = int(time.monotonic() - t0)

    # the claim, minted by the account just before the home starts (it lasts 10 minutes)
    s, b = http("POST", f"{API}/v1/servers/claims", {}, acct.token)
    need(s, b, (200, 201), "mint a claim code")
    code = mask(b["code"])
    text = docker("run", "--rm", "--entrypoint", "cat", "-v", f"{VOL_DATA}:{VOLUME}", HOME_IMAGE, CONFIG).stdout
    text = set_cloud(text, {"enabled": True, "claim_code": code, "slug": SLUG, "name": "CI e2e home",
                            "api": API, "relay_ws": f"wss://{RELAY}/v1/relay/ws"})
    tomllib.loads(text)  # still TOML
    docker("run", "--rm", "-i", "--entrypoint", "sh", "-v", f"{VOL_DATA}:{VOLUME}", HOME_IMAGE, "-c",
           f"cat > {CONFIG}.new && mv {CONFIG}.new {CONFIG}", stdin=text)

    run = ["run", "-d", "--name", CONTAINER, "--network", "host", "-v", f"{VOL_DATA}:{VOLUME}",
           "-v", f"{VOL_MEDIA}:{MEDIA}:ro"]
    if CA_FILE:
        # the node's own CA beside the public ones, for the home's agent (Go reads SSL_CERT_FILE)
        bundle = OUT / "ca-bundle.pem"
        bundle.write_text(Path("/etc/ssl/certs/ca-certificates.crt").read_text() + Path(CA_FILE).read_text())
        run += ["-v", f"{bundle.resolve()}:/etc/maktaba-e2e/ca.pem:ro", "-e", "SSL_CERT_FILE=/etc/maktaba-e2e/ca.pem"]
    docker(*run, HOME_IMAGE)
    wait("the home's health", lambda: http("GET", home.local + "/api/system/health")[0] == 200, 15 * 60, every=5)
    wait("the home's sign-in", lambda: (home.login() or True), 10 * 60, every=10)

    # the library and its scan (setup's roots only feed the folder picker)
    s, b = home.call("POST", "/api/libraries", {"name": "Films", "roots": [MEDIA]})
    need(s, b, (200, 201), "create the library")
    s, b = home.call("GET", "/api/libraries")
    libs = b.get("items", []) if isinstance(b, dict) else []
    lib = next((x["id"] for x in libs if MEDIA in (x.get("roots") or [])), None)
    if lib is None:
        raise AssertionError("the new library is not in GET /api/libraries")

    def found() -> Any:
        home.call("POST", f"/api/libraries/{lib}/scan", {})
        s, b = home.call("GET", f"/api/videos?library={lib}&limit=50")
        items = b.get("items") or [] if isinstance(b, dict) else []
        return items[0] if items else None

    home.video = wait("the test video in the library", found, 10 * 60, every=20)

    # online in the cloud: the agent redeemed the claim and holds its tunnel
    def online() -> Any:
        s, b = http("GET", f"{API}/v1/homes", token=acct.token)
        for h in (b or {}).get("homes", []) if isinstance(b, dict) else []:
            if h.get("slug") == SLUG and h.get("online"):
                return h
        return None

    h = wait("the home online in the account's homes", online, 10 * 60, every=10)
    home.server_id = h["server_id"]
    # A claim gives the new home the Free plan whatever its owner's plan is (v0.3.161: the
    # INSERT takes the column's default); setting the account's plan again reaches every home
    # it owns. Until the cloud copies the plan at claim, the operator does it after a claim.
    if ops("set-plan basic", acct.email) != "plan basic":
        raise AssertionError("cloud-ops set-plan basic after the claim")
    if not str(h.get("relay_url", "")).startswith(HOME):
        raise AssertionError(f"the home's relay address is {h.get('relay_url')}, not {HOME}")
    return f"set up in {setup_sec} s; claimed as {SLUG}; online; relay {HOME}; owned={h.get('owned')}"


def set_cloud(text: str, values: dict[str, Any]) -> str:
    """server.toml with its [cloud] table's keys set (the table setup wrote, or a new one)."""
    def fmt(v: Any) -> str:
        return ("true" if v else "false") if isinstance(v, bool) else json.dumps(v)

    lines = text.splitlines()
    out: list[str] = []
    in_cloud = False
    seen = False
    for line in lines:
        s = line.strip()
        if s.startswith("[") and s.endswith("]"):
            if in_cloud:
                out += [f"  {k} = {fmt(v)}" for k, v in values.items()]
            in_cloud = s == "[cloud]"
            seen = seen or in_cloud
            out.append(line)
            continue
        if in_cloud and "=" in s and s.split("=", 1)[0].strip() in values:
            continue
        out.append(line)
    if in_cloud:
        out += [f"  {k} = {fmt(v)}" for k, v in values.items()]
    if not seen:
        out += ["", "[cloud]"] + [f"  {k} = {fmt(v)}" for k, v in values.items()]
    return "\n".join(out) + "\n"


def check_link(acct: Account, home: Home) -> str:
    wait("the home's cloud link available", lambda: (home.call("GET", "/api/me/cloud-link")[1] or {}).get("available"),
         5 * 60, every=5)
    s, b = home.call("POST", "/api/me/cloud-link/start", {})
    need(s, b, (200, 201), "start the link on the home")
    nonce = mask(b["nonce"])
    s, b = http("POST", f"{API}/v1/homes/{home.server_id}/assertion", {"purpose": "link", "nonce": nonce}, acct.token)
    need(s, b, (200, 201), "the cloud's link assertion")
    s, b = home.call("POST", "/api/me/cloud-link", {"assertion": mask(b["assertion"])})
    need(s, b, (200, 201), "complete the link on the home")
    # the home reports its links to the cloud through its agent (PUT /v1/servers/{id}/links),
    # after the link: the cloud lists the home as linked a moment later
    t0 = time.monotonic()

    def linked() -> bool:
        s, b = http("GET", f"{API}/v1/homes", token=acct.token)
        return any(h.get("slug") == SLUG and h.get("linked") for h in (b or {}).get("homes", []))

    wait("the cloud lists the home as linked", linked, 3 * 60, every=3)
    return f"the home's admin linked to the cloud account; the cloud lists it as linked {time.monotonic() - t0:.0f} s later"


def check_licence(home: Home) -> str:
    """The home asks the cloud for its owner's plan licence every minute; until it holds one
    it trusts, it refuses every request the relay forwards (api/internal/subscriptions)."""
    t0 = time.monotonic()

    def licensed() -> Any:
        s, b = home.call("GET", "/api/entitlements")
        if not (isinstance(b, dict) and (b.get("features") or {}).get("cloud_relay")):
            return None
        # and the relay's gate agrees: an unsigned request is asked to sign in, not refused
        s, _ = http("GET", f"{HOME}/api/videos?limit=1")
        return b if s == 401 else None

    b = wait("the home holds the cloud's licence (signed with the key its release embeds) and "
             "serves the relay", licensed, 4 * 60, every=5)
    return f"tier {b.get('tier')}, remote access on, {time.monotonic() - t0:.0f} s after it came online"


def relay_login(home: Home) -> str:
    """The home's own sign-in, through the relay (what an app does with the home's password)."""
    s, b = http("POST", f"{HOME}/api/auth/login", {"username": "e2e-admin", "password": home.password})
    need(s, b, (200,), "the home's own sign-in through the relay")
    return mask(b["access_token"])


def relay_signin(acct: Account, home: Home) -> str:
    s, b = http("POST", f"{API}/v1/homes/{home.server_id}/assertion", {"purpose": "signin"}, acct.token)
    need(s, b, (200, 201), "the cloud's sign-in assertion")
    s, b = http("POST", f"{HOME}/api/auth/cloud", {"assertion": mask(b["assertion"])})
    need(s, b, (200,), "sign in on the home through the relay")
    return mask(b["access_token"])


class SameOrigin(urllib.request.HTTPRedirectHandler):
    """A media request is answered by the relay itself, never by where it sends the player."""

    def redirect_request(self, req: Any, fp: Any, code: int, msg: str, headers: Any, newurl: str) -> Any:
        raise AssertionError(f"the media URL redirects ({code}) to {urlsplit(newurl).netloc or newurl[:80]}")


MEDIA_OPENER = urllib.request.build_opener(urllib.request.HTTPSHandler(context=CTX), SameOrigin)


def media_url(url: str) -> str:
    """The session's media URL, on the home's relay name: bytes from anywhere else prove nothing."""
    full = urljoin(HOME + "/", url)
    if urlsplit(full)[:2] != urlsplit(HOME)[:2]:
        raise AssertionError(f"the media URL leaves the relay: {urlsplit(full).scheme}://{urlsplit(full).netloc}")
    return full


def play(token: str, home: Home, until: float = 0) -> tuple[int, float]:
    """Plays the whole test video and checks its bytes; with until (a monotonic time), stops there."""
    full = True
    # a profile that plays the test video as it is (with none, "generic" transcodes it to 720p)
    s, b = http("POST", f"{HOME}/api/stream/sessions",
                {"video_id": home.video["id"], "client_profile": "desktop-player"}, token)
    need(s, b, (200, 201), "a stream session through the relay")
    url = b.get("direct_url") or ""
    if b.get("mode") != "direct" or not url:
        raise AssertionError(f"the session is {b.get('mode')}, not direct play")
    t0 = time.monotonic()
    h = hashlib.sha256()
    n = 0
    with MEDIA_OPENER.open(urllib.request.Request(media_url(url)), timeout=120) as r:
        while chunk := r.read(1 << 20):
            h.update(chunk)
            n += len(chunk)
            if until and time.monotonic() > until:
                full = False
                break
    sec = time.monotonic() - t0
    http("DELETE", f"{HOME}/api/stream/sessions/{b.get('session_id')}", token=token)
    if full and h.hexdigest() != home.sha256:
        raise AssertionError("the bytes played through the relay differ from the file")
    return n, sec


def check_relay_play(home: Home) -> str:
    tls = tls_one(f"{SLUG}.{RELAY}")
    token = relay_login(home)
    s, b = http("GET", f"{HOME}/api/videos?limit=5", token=token)
    need(s, b, (200,), "the library through the relay")
    n, sec = play(token, home)
    return f"TLS {tls}; the home's own sign-in through the relay; played {n / 1e6:.1f} MB in {sec:.1f} s ({n * 8 / sec / 1e6:.0f} Mbit/s), bytes match the file"


def check_cloud_play(acct: Account, home: Home) -> str:
    token = relay_signin(acct, home)
    n, sec = play(token, home)
    return f"signed in through the cloud (a sign-in assertion, POST /api/auth/cloud on the relay name); played {n / 1e6:.1f} MB in {sec:.1f} s, bytes match the file"


def check_plans(acct: Account, home: Home) -> str:
    if ops("set-plan free", acct.email) != "plan free":
        raise AssertionError("cloud-ops set-plan free")
    try:
        def refused() -> Any:
            s, b = http("GET", f"{HOME}/api/system/health")
            return (s, b) if s == 402 else None
        s, b = wait("Free: the relay refuses the home", refused, 60, every=3)
        code = b.get("error") if isinstance(b, dict) else b
        if code != "relay_needs_plan":
            raise AssertionError(f"Free: {s} {code}, want 402 relay_needs_plan")
    finally:
        ops("set-plan basic", acct.email)
    wait("Basic: the relay serves the home again", lambda: http("GET", f"{HOME}/api/system/health")[0] == 200, 60, every=3)
    enforce = "true" in node("grep -A1 '^\\[relay.plans\\]' /opt/maktaba-relay/cloud.toml").split("enforce", 1)[-1][:12]
    return f"Free: 402 relay_needs_plan; Basic: served again; speed and stream caps enforced: {enforce}"


def check_restart(home: Home) -> str:
    t0 = time.monotonic()
    node("cd /opt/maktaba-relay && docker compose --env-file .env restart maktaba-cloud", timeout=400)
    restarted = time.monotonic() - t0
    wait("the home back through the relay", lambda: http("GET", f"{HOME}/api/system/health", timeout=10)[0] == 200,
         2 * 60, every=1)
    back = time.monotonic() - t0
    if back > 60:
        raise AssertionError(f"the home answered through the relay {back:.0f} s after its restart began, want within 60 s")
    n, sec = play(relay_login(home), home)
    return f"restart took {restarted:.0f} s; the home answered through the relay {back:.0f} s after it began; played {n / 1e6:.1f} MB"


def check_firewall() -> str:
    ports = sorted(set(range(1, 1025)) | {2019, 2375, 2376, 3000, 5432, 6379, 8080, 8088, 8090, 8091, 8092,
                                          8443, 8445, 9090, 9100, 9187, 54329})

    def is_open(p: int) -> bool:
        try:
            with socket.create_connection((NODE_IP, p), timeout=2):
                return True
        except OSError:
            return False

    with cf.ThreadPoolExecutor(64) as ex:
        open_ports = [p for p, o in zip(ports, ex.map(is_open, ports)) if o]
    if open_ports != [22, 80, 443]:
        raise AssertionError(f"open TCP ports from outside: {open_ports}, want [22, 80, 443]")
    ufw = node("ufw status verbose")
    if "Status: active" not in ufw or "deny (incoming)" not in ufw:
        raise AssertionError("UFW is not active with incoming denied")
    published = node("docker ps --format '{{.Names}} {{.Ports}}'")
    leaks = [l for l in published.splitlines() if "0.0.0.0:" in l and "caddy" not in l]
    if leaks:
        raise AssertionError(f"a container other than Caddy publishes a port: {leaks}")
    return f"{len(ports)} TCP ports probed from outside: only 22, 80, 443 open; UFW active, incoming denied; only Caddy publishes"


def check_backup() -> str:
    if node("systemctl is-enabled maktaba-cloud-backup.timer").strip() != "enabled":
        raise AssertionError("the backup timer is not enabled")
    node("systemctl start maktaba-cloud-backup.service", timeout=600)
    last = node("journalctl -u maktaba-cloud-backup.service -n 5 --no-pager -o cat").strip().splitlines()
    out = node("DEPLOY_DIR=/opt/maktaba-relay /opt/maktaba-relay/ops/backup.sh --check").strip()
    nxt = node("systemctl show maktaba-cloud-backup.timer -p NextElapseUSecRealtime --value").strip()
    return f"{last[-1] if last else 'backup ran'}; {out}; next run {nxt}"


def sample_node(stop: threading.Event, samples: list[dict[str, Any]]) -> None:
    script = ("cat /proc/stat | head -1; grep -E 'MemTotal|MemAvailable' /proc/meminfo; "
              "awk '/eth0:/{print $2, $10}' /proc/net/dev; "
              "docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' | grep -E 'maktaba-cloud|caddy'")
    while not stop.is_set():
        try:
            out = node(script, timeout=30).splitlines()
            cpu = [int(x) for x in out[0].split()[1:]]
            mem_total = int(out[1].split()[1]) * 1024
            mem_avail = int(out[2].split()[1]) * 1024
            rx, tx = (int(x) for x in out[3].split())
            samples.append({"t": time.time(), "cpu": cpu, "mem_used": mem_total - mem_avail, "mem_total": mem_total,
                            "rx": rx, "tx": tx, "containers": out[4:]})
        except Exception as e:  # noqa: BLE001 - a missed sample is a gap, not a failure
            log(f"sample: {e}")
        stop.wait(5)


def check_load(home: Home) -> str:
    players = int(os.environ.get("LOAD_PLAYERS", "8"))
    seconds = int(os.environ.get("LOAD_SECONDS", "60"))
    tokens = [relay_login(home) for _ in range(min(players, 4))]
    total = [0]
    lock = threading.Lock()
    end = time.monotonic() + seconds

    def player(i: int) -> None:
        while time.monotonic() < end:
            try:
                n, _ = play(tokens[i % len(tokens)], home, until=end)
                with lock:
                    total[0] += n
            except Exception as e:  # noqa: BLE001 - a failed stream is counted below
                log(f"player {i}: {e}")
                with lock:
                    total.append(-1)
                time.sleep(1)

    samples: list[dict[str, Any]] = []
    stop = threading.Event()
    sampler = threading.Thread(target=sample_node, args=(stop, samples), daemon=True)
    sampler.start()
    time.sleep(6)
    t0 = time.monotonic()
    with cf.ThreadPoolExecutor(players) as ex:
        list(ex.map(player, range(players)))
    wall = time.monotonic() - t0
    time.sleep(6)
    stop.set()
    sampler.join(30)
    failures = total.count(-1)
    if len(samples) < 3:
        raise AssertionError(f"only {len(samples)} samples of the node")
    first, last = samples[1], samples[-1]
    dt = last["t"] - first["t"]
    busy = lambda c: sum(c) - c[3] - c[4]  # noqa: E731 - user+nice+system+irq+softirq+steal
    cpu_pct = 100 * (busy(last["cpu"]) - busy(first["cpu"])) / max(1, sum(last["cpu"]) - sum(first["cpu"]))
    rx = (last["rx"] - first["rx"]) * 8 / dt / 1e6
    tx = (last["tx"] - first["tx"]) * 8 / dt / 1e6
    relayed = total[0] * 8 / wall / 1e6 if wall else 0
    peak_mem = max(s["mem_used"] for s in samples)
    sizing = {
        "node": os.environ.get("NODE_PLAN", ""), "players": players, "seconds": round(wall),
        "relayed_mbit_s": round(relayed, 1), "node_rx_mbit_s": round(rx, 1), "node_tx_mbit_s": round(tx, 1),
        "node_cpu_pct": round(cpu_pct, 1), "node_mem_used_mb": round(peak_mem / 2**20),
        "node_mem_total_mb": round(samples[0]["mem_total"] / 2**20), "failed_streams": failures,
        "containers_last": last["containers"],
        "cpu_pct_per_100_mbit": round(cpu_pct / relayed * 100, 1) if relayed else None,
    }
    (OUT / "sizing.json").write_text(json.dumps(sizing, indent=2))
    if failures:
        raise AssertionError(f"{failures} streams failed under load: {sizing}")
    return (f"{players} players for {wall:.0f} s: relayed {relayed:.0f} Mbit/s; node CPU {cpu_pct:.0f} % "
            f"(about {sizing['cpu_pct_per_100_mbit']} % per 100 Mbit/s), memory {sizing['node_mem_used_mb']} of "
            f"{sizing['node_mem_total_mb']} MB, NIC in {rx:.0f} / out {tx:.0f} Mbit/s")


def stop_home() -> None:
    """The home's log kept, its container and volumes gone from the runner."""
    p = docker("logs", "--tail", "400", CONTAINER, check_rc=False)
    (OUT / "home.log").write_text((p.stdout or "") + (p.stderr or ""), encoding="utf-8")
    docker("rm", "-f", CONTAINER, check_rc=False)
    docker("volume", "rm", "-f", VOL_DATA, VOL_MEDIA, check_rc=False)


def check_ended(acct: Account) -> str:
    """What the test registered is ended, whatever failed before: the account as its owner would
    (signed in, its password confirmed) when it got that far, then every test account through the
    node (one whose confirmation or plan failed, or a run that stopped early). None may be left
    active, and no home linked; the 30-day deletion hold is the cloud's own rule."""
    own = ""
    own_error = ""
    if acct.token:
        try:
            acct.login()
            s, b = http("DELETE", f"{API}/v1/account/me", {"password": acct.password}, acct.token)
            need(s, b, (204,), "the account's own deletion")
            own = "the account deleted by its owner; "
        except Exception as e:  # noqa: BLE001 - the node ends it below; the check still fails on this
            own_error = str(e)[:300]
    got = json.loads(node("/opt/maktaba-relay/ops/cloud-ops.py end-test-accounts"))
    if got["left_active"] or got["left_homes"]:
        raise AssertionError(f"left behind: {got['left_active']} active test accounts, {got['left_homes']} homes")
    s, _ = http("GET", f"{HOME}/api/system/health")
    if s == 200:
        raise AssertionError("the test home still answers through the relay")
    if own_error:
        raise AssertionError(f"the account's own deletion failed (the node ended it instead): {own_error}")
    return f"{own}{got['ended']} ended by the node, {got['homes_removed']} homes removed; none left active, no home linked"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--mode", choices=("full", "smoke"), default="full")
    mode = ap.parse_args().mode
    OUT.mkdir(parents=True, exist_ok=True)
    if os.environ.get("PIN_HOSTS") == "1":
        pin_hosts()
    acct = Account()
    home = Home()
    done = threading.Event()
    threading.Thread(target=keep_renewing, args=(done,), daemon=True).start()
    try:
        check("tls", check_tls)
        check("health", check_health)
        check("firewall", check_firewall)
        check("backup", check_backup)
        a = check("account", lambda: check_account(acct))
        check("broadcast-off", lambda: check_broadcast_off(acct), needs=(a,))
        h = check("home", lambda: make_home(acct, home), needs=(a,))
        lc = check("licence", lambda: check_licence(home), needs=(h,))
        p = check("relay-play", lambda: check_relay_play(home), needs=(lc,))
        check("plans", lambda: check_plans(acct, home), needs=(p,))
        lk = check("link", lambda: check_link(acct, home), needs=(lc,))
        check("cloud-play", lambda: check_cloud_play(acct, home), needs=(lk,))
        if mode == "full":
            check("restart", lambda: check_restart(home), needs=(p,))
            check("load", lambda: check_load(home), needs=(p,))
    finally:
        stop_home()
        check("ended", lambda: check_ended(acct))
        done.set()
        (OUT / "results.json").write_text(json.dumps(results, indent=2))
        mark = {True: "✅", False: "❌", None: "⏭️"}
        lines = [f"| {mark[r['ok']]} | {r['check']} | {r['sec']} s | {r['detail']} |" for r in results]
        summary = "\n".join(["| | check | time | result |", "|---|---|---|---|", *lines])
        (OUT / "summary.md").write_text(summary + "\n")
        if os.environ.get("GITHUB_STEP_SUMMARY"):
            with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as f:
                f.write(f"### Cloud e2e ({mode}) on {RELAY}\n\n{summary}\n\n")
    return 0 if results and all(r["ok"] for r in results) else 1


if __name__ == "__main__":
    sys.exit(main())
