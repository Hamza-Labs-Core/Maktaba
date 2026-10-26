// Sign in a TV: the signed-in person approves the code the TV shows.
//
// Real contract (api/internal/handlers/discovery/device.go):
//   POST /api/pairing/approve  { code: "ABCD-1234" } → 200 { status: "approved" }
//     422 a malformed code; 409 the code was already approved.
//
// The TV shows the code and a QR code for <home>/pair?code=ABCD-1234, so the
// code comes prefilled from the URL; the person can also type it. The TV is
// then signed in as this person, with this person's own role.
import { type FormEvent, useState } from "react";
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

/** The code as the TV shows it (ABCD-1234), or null when it cannot be one. */
export function normalizePairCode(raw: string): string | null {
  const c = raw.replace(/[\s-]+/g, "").toUpperCase();
  return CODE_RE.test(c) ? `${c.slice(0, 4)}-${c.slice(4)}` : null;
}

type Outcome = "approved" | "used" | "invalid" | { error: string } | null;

export function Pair() {
  const { t } = useI18n();
  const { user } = useAuth();
  const [params] = useSearchParams();
  const [code, setCode] = useState(() => params.get("code") ?? "");
  const [busy, setBusy] = useState(false);
  const [outcome, setOutcome] = useState<Outcome>(null);

  async function approve(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const normalized = normalizePairCode(code);
    if (!normalized) {
      setOutcome("invalid");
      return;
    }
    setBusy(true);
    setOutcome(null);
    try {
      await api.post("/api/pairing/approve", { code: normalized });
      setOutcome("approved");
    } catch (err) {
      if (err instanceof ApiError && err.status === 409) setOutcome("used");
      else if (err instanceof ApiError && err.status === 422) setOutcome("invalid");
      else
        setOutcome({
          error:
            err instanceof ApiError ? err.problem.detail || err.problem.title : t("common.error"),
        });
    } finally {
      setBusy(false);
    }
  }

  const who = bidiIsolate(user?.username ?? "");

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
        ) : (
          <form className="mkt-form" onSubmit={approve} noValidate>
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
              error={
                outcome === "invalid"
                  ? t("pair.invalid")
                  : outcome === "used"
                    ? t("pair.used")
                    : outcome && typeof outcome === "object"
                      ? outcome.error
                      : undefined
              }
            />
            <p className="mkt-muted">{t("pair.note")}</p>
            <Button type="submit" loading={busy} disabled={code.trim() === ""}>
              {t("pair.approve")}
            </Button>
          </form>
        )}
      </Card>
    </section>
  );
}
