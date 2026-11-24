#!/usr/bin/env python3
"""Operator actions on one Maktaba Cloud node (docs/cloud-single-node.md, "Operator actions").

Run on the node, as root, from the CI deploy (over SSH) or by hand:

    cloud-ops.py verify-email   < email        confirm an account's email (no mail is sent)
    cloud-ops.py set-plan PLAN  < email        set an account's plan (free, basic, pro)
    cloud-ops.py account        < email        print the account's id, plan and whether confirmed
    cloud-ops.py end-test-accounts             end what the deploy's tests left (below)

The account's email is read from stdin, never from the command line, so it is in no process
list, shell history or CI log; this tool prints no email, password, token or code.

- verify-email: the cloud's only mail driver writes mail to its log, which redacts the address
  and the link's code, so a sign-up's confirmation can't reach anyone. Until the cloud sends
  mail, the operator confirms an account here: the change and its audit row ("cli",
  auth.email_verified) in one transaction.
- end-test-accounts: the accounts of the deploy's tests (ci-e2e-<digits>@example.com, nothing
  else): their homes and claim codes deleted, their sessions ended, their 30-day deletion holds on, in one
  transaction, audited. It prints how many it ended and how many are left active or with a home
  (the tests fail unless none is). A test that stopped early is ended by the next deploy's.
- set-plan: through the operator console's API (story 50.33) on the stack's private network
  (172.30.77.10:8091, Host admin.internal), signed in as this node's own staff account
  (cloud-ops@maktaba.tv, service_admin): made on first use with `maktaba-cloud staff add`, its
  password and TOTP secret kept in /opt/maktaba-relay/ops-state/staff.json (root, 0600), which
  no container mounts. The plan change is audited by the cloud as that staff account.
"""
from __future__ import annotations

import base64
import hashlib
import hmac
import json
import os
import secrets
import struct
import subprocess
import sys
import time
import urllib.error
import urllib.request

DEPLOY_DIR = os.environ.get("DEPLOY_DIR", "/opt/maktaba-relay")
STATE_DIR = os.path.join(DEPLOY_DIR, "ops-state")
STATE = os.path.join(STATE_DIR, "staff.json")
ADMIN = "http://172.30.77.10:8091"
ADMIN_HOST = "admin.internal"
STAFF_EMAIL = "cloud-ops@maktaba.tv"
PLANS = ("free", "basic", "pro")


def die(msg: str) -> None:
    print(f"cloud-ops: {msg}", file=sys.stderr)
    sys.exit(1)


def env_value(key: str) -> str:
    with open(os.path.join(DEPLOY_DIR, ".env"), encoding="utf-8") as f:
        for line in f:
            if line.startswith(key + "="):
                return line.split("=", 1)[1].strip()
    return ""


def compose(*args: str, stdin: str | None = None) -> str:
    p = subprocess.run(
        ["docker", "compose", "--env-file", ".env", *args],
        cwd=DEPLOY_DIR, input=stdin, capture_output=True, text=True, check=False,
    )
    if p.returncode != 0:
        # the command's own words, which never hold the email (it is a psql variable on stdin)
        die(f"docker compose {args[0]} failed: {p.stderr.strip()[-400:]}")
    return p.stdout


def psql(sql: str, email: str) -> str:
    """Runs sql with :'email' bound to email, both on stdin (never an argument)."""
    user = env_value("POSTGRES_USER") or "maktaba"
    db = env_value("POSTGRES_DB") or "maktaba"
    # psql's \set takes the value verbatim up to the line's end; an email holds no newline
    if "\n" in email or "\r" in email:
        die("the email holds a line break")
    script = "\\set ON_ERROR_STOP on\n\\set email '" + email.replace("'", "''") + "'\n" + sql
    return compose("exec", "-T", "postgres", "psql", "-X", "-q", "-A", "-t", "-U", user, "-d", db, stdin=script)


def read_email() -> str:
    email = sys.stdin.readline().strip()
    if not email or "@" not in email or len(email) > 320:
        die("give the account's email on stdin")
    return email


# --- accounts -------------------------------------------------------------------------------

def account(email: str) -> None:
    out = psql("SELECT id, plan, email_verified FROM users WHERE lower(email) = lower(:'email');\n", email).strip()
    if not out:
        die("no account with that email")
    uid, plan, verified = out.split("|")
    print(json.dumps({"id": uid, "plan": plan, "email_verified": verified == "t"}))


def verify_email(email: str) -> None:
    out = psql(
        """BEGIN;
WITH u AS (
  UPDATE users SET email_verified = true, updated_at = now()
  WHERE lower(email) = lower(:'email') AND NOT email_verified
  RETURNING id
)
INSERT INTO audit_log (actor_kind, action, target_kind, target_id, detail)
SELECT 'cli', 'auth.email_verified', 'user', id::text,
       '{"by":"cloud-ops verify-email","why":"the cloud sends no mail"}'
FROM u
RETURNING target_id;
COMMIT;
SELECT email_verified FROM users WHERE lower(email) = lower(:'email');
""",
        email,
    ).split()
    if not out:
        die("no account with that email")
    if out[-1] != "t":
        die("the account is still not confirmed")
    print("confirmed" if len(out) > 1 else "already confirmed")


# a deploy's test account (tests/e2e.py), never a person's
TEST_EMAIL = r"^ci-e2e-[0-9]+@example\.com$"


def end_test_accounts() -> None:
    out = psql(
        f"""BEGIN;
-- each account's lock first, as a claim takes it: a home redeeming its code now is either in
-- already (and deleted below) or finds its code gone
CREATE TEMP TABLE t ON COMMIT DROP AS SELECT id FROM users WHERE email ~ '{TEST_EMAIL}' FOR NO KEY UPDATE;
DELETE FROM server_claims WHERE user_id IN (SELECT id FROM t);
CREATE TEMP TABLE n ON COMMIT DROP AS
  SELECT t.id, EXISTS (SELECT 1 FROM servers s WHERE s.owner_user_id = t.id) AS homes,
         NOT EXISTS (SELECT 1 FROM account_deletions d WHERE d.user_id = t.id AND d.cancelled_at IS NULL) AS active
    FROM t;
DELETE FROM servers WHERE owner_user_id IN (SELECT id FROM t);
UPDATE sessions SET revoked_at = now() WHERE user_id IN (SELECT id FROM t) AND revoked_at IS NULL;
INSERT INTO account_deletions (user_id, purge_after)
  SELECT id, now() + INTERVAL '30 days' FROM n WHERE active
  ON CONFLICT (user_id) DO UPDATE SET requested_at = now(), purge_after = now() + INTERVAL '30 days', cancelled_at = NULL;
INSERT INTO audit_log (actor_kind, action, target_kind, target_id, detail)
  SELECT 'cli', 'account.delete', 'user', id::text, '{{"by":"cloud-ops end-test-accounts"}}' FROM n WHERE active OR homes;
SELECT (SELECT count(*) FROM n WHERE active), (SELECT count(*) FROM n WHERE homes);
COMMIT;
SELECT (SELECT count(*) FROM users u WHERE u.email ~ '{TEST_EMAIL}'
          AND NOT EXISTS (SELECT 1 FROM account_deletions d WHERE d.user_id = u.id AND d.cancelled_at IS NULL)),
       (SELECT count(*) FROM servers s JOIN users u ON u.id = s.owner_user_id WHERE u.email ~ '{TEST_EMAIL}');
""",
        "",
    ).split()
    if len(out) != 2:
        die("ending the test accounts printed no counts")
    ended, left = (tuple(int(x) for x in row.split("|")) for row in out)
    print(json.dumps({"ended": ended[0], "homes_removed": ended[1], "left_active": left[0], "left_homes": left[1]}))


# --- the node's staff account ---------------------------------------------------------------

def totp(secret_b32: str, step: int) -> str:
    key = base64.b32decode(secret_b32.upper() + "=" * (-len(secret_b32) % 8))
    mac = hmac.new(key, struct.pack(">Q", step), hashlib.sha1).digest()
    o = mac[-1] & 0x0F
    return f"{(struct.unpack('>I', mac[o:o + 4])[0] & 0x7FFFFFFF) % 1_000_000:06d}"


def fresh_code(state: dict) -> str:
    """A code from a 30 s step not used before (a step's code works once)."""
    step = int(time.time()) // 30
    if step <= state.get("last_step", 0):
        time.sleep(30 - time.time() % 30 + 1)
        step = int(time.time()) // 30
    state["last_step"] = step
    save(state)
    return totp(state["totp_secret"], step)


def call(method: str, path: str, body: dict | None = None, token: str = "") -> tuple[int, dict]:
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Host", ADMIN_HOST)
    req.add_header("Content-Type", "application/json")
    req.add_header("Origin", "https://" + ADMIN_HOST)
    req.add_header("X-Maktaba-Staff", "1")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            raw = r.read()
            return r.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as e:
        raw = e.read()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {}


def save(state: dict) -> None:
    os.makedirs(STATE_DIR, mode=0o700, exist_ok=True)
    tmp = STATE + ".tmp"
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(state, f)
    os.replace(tmp, STATE)


def enrol(sub: str) -> dict:
    """staff add (or reset-mfa) prints an enrolment link; enrol with a new password and TOTP."""
    out = compose("exec", "-T", "api", "/usr/local/bin/maktaba-cloud", "staff", sub,
                  "--config", "/etc/maktaba/cloud/cloud.toml",
                  "--email", STAFF_EMAIL, *(["--role", "service_admin"] if sub == "add" else []))
    link = next((w for w in out.split() if "/enroll#" in w), "")
    if not link:
        die(f"staff {sub} printed no enrolment link")
    tok = link.split("#", 1)[1]
    code, r = call("POST", "/v1/staff/enroll/start", {"token": tok})
    if code != 200 or "totp_secret" not in r:
        die(f"enrolment start: {code} {r.get('error', '')}")
    state = {"password": secrets.token_urlsafe(30), "totp_secret": r["totp_secret"], "last_step": 0}
    save(state)
    code, r = call("POST", "/v1/staff/enroll/finish",
                   {"token": tok, "password": state["password"], "code": fresh_code(state)})
    if code not in (200, 201, 204):
        die(f"enrolment finish: {code} {r.get('error', '')}")
    return state


def staff_token() -> str:
    state = None
    if os.path.exists(STATE):
        with open(STATE, encoding="utf-8") as f:
            state = json.load(f)
    if state is None:
        # a new node (or a new database): make the account, or give the one there a new factor
        try:
            state = enrol("add")
        except SystemExit:
            state = enrol("reset-mfa")
    for attempt in (1, 2):
        code, r = call("POST", "/v1/staff/login", {"email": STAFF_EMAIL, "password": state["password"]})
        if code == 200 and r.get("mfa_token"):
            code, r = call("POST", "/v1/staff/mfa/totp", {"mfa_token": r["mfa_token"], "code": fresh_code(state)})
            if code == 200 and r.get("access_token"):
                return r["access_token"]
        if attempt == 1:
            # the state no longer matches the database (restored, or rebuilt): enrol again
            state = enrol("reset-mfa")
    die(f"the node's staff account can't sign in ({code} {r.get('error', '')})")
    return ""


def set_plan(plan: str, email: str) -> None:
    if plan not in PLANS:
        die(f"plan is one of {', '.join(PLANS)}")
    out = psql("SELECT id FROM users WHERE lower(email) = lower(:'email');\n", email).strip()
    if not out:
        die("no account with that email")
    tok = staff_token()
    code, r = call("POST", f"/v1/admin/accounts/{out}/plan",
                   {"plan": plan, "reason": "cloud-ops set-plan (the node's operator)"}, tok)
    if code not in (200, 204):
        die(f"set plan: {code} {r.get('error', '')}")
    print(f"plan {plan}")


def main() -> None:
    if os.geteuid() != 0:
        die("run as root on the node")
    args = sys.argv[1:]
    if args[:1] == ["verify-email"] and len(args) == 1:
        verify_email(read_email())
    elif args[:1] == ["set-plan"] and len(args) == 2:
        set_plan(args[1], read_email())
    elif args[:1] == ["account"] and len(args) == 1:
        account(read_email())
    elif args == ["end-test-accounts"]:
        end_test_accounts()
    else:
        die("usage: cloud-ops.py verify-email | set-plan PLAN | account (the email on stdin) | end-test-accounts")


if __name__ == "__main__":
    main()
