-- store-accounts (story 54.15 §3, §4): store accounts, their factors, sessions, roles and
-- the audit trail. The store's own: nothing here is shared with Maktaba Cloud.
CREATE TABLE IF NOT EXISTS accounts (
  id                TEXT PRIMARY KEY,
  email             TEXT NOT NULL UNIQUE,          -- lower case
  email_verified_at TEXT,
  display_name      TEXT NOT NULL DEFAULT '',
  password_hash     TEXT,                          -- argon2id; NULL for a passkey-only account
  locale            TEXT NOT NULL DEFAULT 'en' CHECK (locale IN ('en', 'ar')),
  status            TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'deleted')),
  created           TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS account_mfa (
  id            TEXT PRIMARY KEY,
  account       TEXT NOT NULL REFERENCES accounts (id),
  kind          TEXT NOT NULL CHECK (kind IN ('totp', 'passkey')),
  name          TEXT NOT NULL DEFAULT '',
  credential_id TEXT UNIQUE,                       -- a passkey's id, base64url
  secret        TEXT NOT NULL,                     -- a passkey's COSE public key (base64url), or the TOTP secret encrypted with MFA_ENC_KEY
  alg           INTEGER,                           -- COSE algorithm of a passkey (-7 ES256, -8 EdDSA)
  sign_count    INTEGER NOT NULL DEFAULT 0,
  last_step     INTEGER NOT NULL DEFAULT 0,        -- the last TOTP step used (a code is refused a second time)
  added         TEXT NOT NULL,
  last_used     TEXT
);
CREATE INDEX IF NOT EXISTS account_mfa_account ON account_mfa (account);

CREATE TABLE IF NOT EXISTS recovery_codes (
  account   TEXT NOT NULL REFERENCES accounts (id),
  code_hash TEXT NOT NULL,
  used_at   TEXT,
  PRIMARY KEY (account, code_hash)
);

CREATE TABLE IF NOT EXISTS sessions (
  id_hash    TEXT PRIMARY KEY,                     -- sha256 of the cookie's value; the value is never stored
  account    TEXT NOT NULL REFERENCES accounts (id),
  created    TEXT NOT NULL,
  last_seen  TEXT NOT NULL,
  mfa_at     TEXT NOT NULL,                        -- the last time a factor was confirmed (re-auth)
  mfa_kind   TEXT NOT NULL CHECK (mfa_kind IN ('password', 'totp', 'passkey', 'recovery')),
  user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS sessions_account ON sessions (account);

CREATE TABLE IF NOT EXISTS email_tokens (
  token_hash TEXT PRIMARY KEY,
  account    TEXT NOT NULL REFERENCES accounts (id),
  purpose    TEXT NOT NULL CHECK (purpose IN ('verify', 'change')),
  email      TEXT NOT NULL,
  expires    TEXT NOT NULL,
  used_at    TEXT
);

CREATE TABLE IF NOT EXISTS webauthn_challenges (
  id        TEXT PRIMARY KEY,
  account   TEXT,                                  -- NULL for a sign-in by a discoverable passkey
  challenge TEXT NOT NULL,
  purpose   TEXT NOT NULL CHECK (purpose IN ('register', 'signin', 'reauth')),
  expires   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS roles (
  account    TEXT NOT NULL REFERENCES accounts (id),
  role       TEXT NOT NULL CHECK (role IN ('buyer', 'developer', 'store_reviewer', 'store_admin')),
  granted_by TEXT NOT NULL,
  at         TEXT NOT NULL,
  PRIMARY KEY (account, role)
);

-- a store admin's reset of an account's factors waits 7 days (54.15 §4)
CREATE TABLE IF NOT EXISTS mfa_resets (
  account      TEXT PRIMARY KEY REFERENCES accounts (id),
  requested_by TEXT NOT NULL,
  requested_at TEXT NOT NULL,
  completes_at TEXT NOT NULL,
  done_at      TEXT,
  cancelled_at TEXT
);

CREATE TABLE IF NOT EXISTS bootstrap_used (
  token_hash TEXT PRIMARY KEY,
  account    TEXT NOT NULL,
  at         TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  actor     TEXT NOT NULL,                         -- an account id, 'home:<id>', 'system'
  action    TEXT NOT NULL,
  target    TEXT NOT NULL DEFAULT '',
  at        TEXT NOT NULL,
  ip_prefix TEXT NOT NULL DEFAULT '',              -- /24 or /64, never the whole address
  result    TEXT NOT NULL                          -- 'ok' or a refusal code
);
CREATE INDEX IF NOT EXISTS audit_at ON audit (at);
