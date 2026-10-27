// Sign in a device by code (Epic 14 for the TVs, Epic 42 story 42.6 for every app): the
// signed-in person sees what is asking, then approves it.
//
// Real contract (api/internal/handlers/discovery/device.go):
//   GET  /api/pairing/device/{code} → 200 { code, client_app, platform, device,
//                                       device_label, expires_at }; 404 not waiting
//   POST /api/pairing/approve  { code: "ABCD-1234" } → 200 { status: "approved" }
//     422 a malformed code; 409 the code was already approved; 404 not waiting.
//
// An app shows the code and a QR code for <home>/pair?code=ABCD-1234, so the code can
// come prefilled from the URL (and is then looked up at once); the person can also type
// it. Approving is always an explicit click, after the page shows what is asking. The
// device is then signed in as this person, with this person's own role.
import { type FormEvent, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { TitleRow } from "../help/HelpLink";

// Crockford base-32: digits and A-Z without I, L, O, U.
const CODE_RE = /^[0-9A-HJKMNP-TV-Z]{8}$/;

/** The code as the device shows it (ABCD-1234), or null when it cannot be one. */
export function normalizePairCode(raw: string): string | null {
  const c = raw.replace(/[\s-]+/g, "").toUpperCase();
  return CODE_RE.test(c) ? `${c.slice(0, 4)}-${c.slice(4)}` : null;
}

/** What is asking to sign in with a code (GET /api/pairing/device/{code}). */
export interface PairRequest {
  code: string;
  client_app: string;
  platform: string;
  device: string;
  device_label: string;
  expires_at: string;
}

// Platform names are product names, the same in every language.
const PLATFORMS: Record<string, string> = {
  ios: "iOS",
  ipados: "iPadOS",
  macos: "macOS",
  tvos: "tvOS",
  android: "Android",
  androidtv: "Android TV",
  windows: "Windows",
  linux: "Linux",
  web: "Web",
};

type Outcome = "approved" | "used" | "invalid" | "notHeld" | { error: string } | null;

function problemText(err: unknown, fallback: string): string {
  return err instanceof ApiError ? err.problem.detail || err.problem.title : fallback;
}

export function Pair() {
  const { t, formatDate } = useI18n();
  const { user } = useAuth();
  const [params] = useSearchParams();
  const [code, setCode] = useState(() => params.get("code") ?? "");
  const [busy, setBusy] = useState(false);
  const [asking, setAsking] = useState<PairRequest | null>(null);
  const [outcome, setOutcome] = useState<Outcome>(null);

  async function lookUp(raw: string) {
    const normalized = normalizePairCode(raw);
    if (!normalized) {
      setOutcome("invalid");
      return;
    }
    setBusy(true);
    setOutcome(null);
    try {
      setAsking(
        await api.get<PairRequest>(`/api/pairing/device/${encodeURIComponent(normalized)}`)
      );
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) setOutcome("notHeld");
      else setOutcome({ error: problemText(err, t("common.error")) });
    } finally {
      setBusy(false);
    }
  }

  // a code from the URL (the app's QR code) is looked up at once; approving still waits
  // for the person's click
  useEffect(() => {
    const fromUrl = params.get("code");
    if (fromUrl && normalizePairCode(fromUrl)) void lookUp(fromUrl);
  }, []);

  function onCheck(e: FormEvent) {
    e.preventDefault();
    if (!busy) void lookUp(code);
  }

  async function approve() {
    if (busy || !asking) return;
    setBusy(true);
    setOutcome(null);
    try {
      await api.post("/api/pairing/approve", { code: asking.code });
      setOutcome("approved");
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setOutcome("used");
      else if (err instanceof ApiError && err.status === 404) setOutcome("notHeld");
      else if (err instanceof ApiError && err.status === 422) setOutcome("invalid");
      else setOutcome({ error: problemText(err, t("common.error")) });
      setAsking(null);
    } finally {
      setBusy(false);
    }
  }

  function appName(r: PairRequest): string {
    const key = `pair.app.${r.client_app || "unknown"}`;
    const label = t(key);
    return label === key ? r.client_app : label;
  }

  function deviceKind(r: PairRequest): string {
    if (!r.device) return "";
    const key = `pair.device.${r.device}`;
    const label = t(key);
    return label === key ? r.device : label;
  }

  const who = bidiIsolate(user?.username ?? "");
  const message =
    outcome === "invalid"
      ? t("pair.invalid")
      : outcome === "used"
        ? t("pair.used")
        : outcome === "notHeld"
          ? t("pair.notHeld")
          : outcome && typeof outcome === "object"
            ? outcome.error
            : undefined;

  return (
    <section className="mkt-page mkt-pair">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("pair.title")}</h1>
        </TitleRow>
      </header>

      <Card>
        {outcome === "approved" ? (
          <p role="status" data-testid="pair-approved">
            {t("pair.approved")}
          </p>
        ) : asking ? (
          <div className="mkt-pair__asking" data-testid="pair-asking">
            <p>
              {t("pair.asking", {
                app: bidiIsolate(appName(asking)),
                name: who,
              })}
            </p>
            <dl className="mkt-pair__details">
              {asking.device_label ? (
                <>
                  <dt>{t("pair.details.label")}</dt>
                  <dd>{bidiIsolate(asking.device_label)}</dd>
                </>
              ) : null}
              {asking.platform ? (
                <>
                  <dt>{t("pair.details.platform")}</dt>
                  <dd>
                    {bidiIsolate(PLATFORMS[asking.platform] ?? asking.platform)}
                    {asking.device ? ` · ${deviceKind(asking)}` : ""}
                  </dd>
                </>
              ) : null}
              <dt>{t("pair.code")}</dt>
              <dd className="mkt-mono" dir="ltr">
                {asking.code}
              </dd>
              <dt>{t("pair.details.expires")}</dt>
              <dd>{formatDate(asking.expires_at)}</dd>
            </dl>
            <p className="mkt-muted">{t("pair.note")}</p>
            <div className="mkt-pair__actions">
              <Button onClick={() => void approve()} loading={busy}>
                {t("pair.approve")}
              </Button>
              <Button
                variant="ghost"
                disabled={busy}
                onClick={() => {
                  setAsking(null);
                  setOutcome(null);
                }}
              >
                {t("common.cancel")}
              </Button>
            </div>
          </div>
        ) : (
          <form className="mkt-form" onSubmit={onCheck} noValidate>
            <p>{t("pair.intro", { name: who })}</p>
            <Input
              label={t("pair.code")}
              value={code}
              onChange={(e) => {
                setCode(e.target.value);
                if (outcome) setOutcome(null);
              }}
              placeholder="ABCD-1234"
              autoComplete="off"
              autoCapitalize="characters"
              spellCheck={false}
              dir="ltr"
              className="mkt-mono"
              error={message}
            />
            <Button type="submit" loading={busy} disabled={code.trim() === ""}>
              {t("pair.check")}
            </Button>
          </form>
        )}
      </Card>
    </section>
  );
}
