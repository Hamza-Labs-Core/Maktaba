// /tv — approve a TV's Maktaba Cloud code in a browser (story 58.03).
//
// A TV with no account anywhere shows an 8-character code (ABCD-2345) and a QR of
// https://maktaba.tv/tv?c=ABCD2345, which forwards here. Signed in to Maktaba Cloud (the route
// asks first), the person reads what the TV is (GET /v1/tv/codes/{code}: its platform and
// country), picks one of their homes that is linked to this cloud account, and confirms: the
// cloud asks that home over its link to approve the code as the linked user
// (POST /v1/tv/codes/{code}/approve {server_id}). The TV then signs in to the home, play-scoped
// like every linked sign-in. Cancel approves nothing and sends nothing.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Radio, RadioGroup } from "@ds/components/Choice/Radio";
import { api, CloudApiError } from "../shared/api";
import { useI18n } from "../shared/i18n";
import { Banner, LoadError, Loading, Page, useErrorMessage, useLoad } from "../shared/ui";
import type { CloudHome } from "./Homes";

/** The cloud code alphabet: story 15.06's pairing alphabet without 0 and 1. */
export const TV_CODE_ALPHABET = "23456789ABCDEFGHJKMNPQRSTVWXYZ";
const CODE_RE = new RegExp(`^[${TV_CODE_ALPHABET}]{8}$`);

/** Why a typed code is not a code, or null when it is one. */
export function tvCodeProblem(raw: string): "empty" | "length" | "chars" | null {
  const s = raw.replace(/[\s-]/g, "").toUpperCase();
  if (!s) return "empty";
  if ([...s].some((ch) => !TV_CODE_ALPHABET.includes(ch))) return "chars";
  if (s.length !== 8) return "length";
  return null;
}

/** A typed code as the API takes it (ABCD2345): any case, with or without the dash. */
export function parseTvCode(raw: string | null): string | null {
  if (raw == null) return null;
  const s = raw.replace(/[\s-]/g, "").toUpperCase();
  return CODE_RE.test(s) ? s : null;
}

// The TV platforms a cloud code comes from, by the maker's name: product names, the same in
// every language (the home app's /pair reads them the same way).
const TV_PLATFORMS: Record<string, string> = {
  tizen: "Samsung",
  webos: "LG",
  roku: "Roku",
  firetv: "Fire",
  androidtv: "Android",
  tvos: "Apple",
  cast: "Google",
  vidaa: "Hisense",
};

/** A TV platform's maker (tizen → Samsung), or the platform as sent. */
export function tvPlatformName(platform: string): string {
  return TV_PLATFORMS[platform] ?? platform;
}

/** A country code as the reader's language names it (JO → Jordan, الأردن); "" for none. */
export function countryName(code: string, locale: string): string {
  if (!code) return "";
  if (!/^[A-Za-z]{2}$/.test(code)) return code;
  try {
    return new Intl.DisplayNames([locale], { type: "region" }).of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

/** Keeps a Latin name in an Arabic sentence (and the reverse) from reordering its neighbours. */
const isolate = (s: string) => `⁨${s}⁩`;

/** GET /v1/tv/codes/{code}. */
export interface TvCode {
  code: string;
  platform: string;
  app_version: string;
  country: string;
  expires_at: string;
}

/** POST /v1/tv/codes/{code}/approve. */
interface Approved {
  status: string;
  platform: string;
  country: string;
  home_name: string;
}

// The error codes the two calls answer with their own copy (tv.error.lookup.* / tv.error.approve.*).
const LOOKUP_ERRORS = new Set(["not_found", "rate_limited"]);
const APPROVE_ERRORS = new Set([
  "not_found",
  "not_linked",
  "home_offline",
  "home_unreachable",
  "already_approved",
  "rate_limited",
]);
// After these the code is gone: approving it again cannot work, so the page offers a new code.
const CODE_GONE = new Set(["not_found", "already_approved"]);

function useTvErrorMessage(kind: "lookup" | "approve") {
  const { t } = useI18n();
  const generic = useErrorMessage();
  const known = kind === "lookup" ? LOOKUP_ERRORS : APPROVE_ERRORS;
  return (err: unknown): string => {
    if (err instanceof CloudApiError && known.has(err.code)) {
      if (err.code === "rate_limited" && err.retryAfter) {
        return t("tv.error.rate_limited_wait", { seconds: err.retryAfter });
      }
      return t(`tv.error.${kind}.${err.code}`);
    }
    return generic(err);
  };
}

export function ApproveTv() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const raw = params.get("c");
  const code = parseTvCode(raw);

  const enter = (next: string | null) => setParams(next ? { c: next } : {}, { replace: false });

  return (
    <Page title={t("tv.title")}>
      {code ? (
        <Confirm key={code} code={code} onOther={() => enter(null)} />
      ) : (
        <CodeForm initial={raw ?? ""} onCode={enter} />
      )}
    </Page>
  );
}

function CodeForm({ initial, onCode }: { initial: string; onCode: (code: string) => void }) {
  const { t } = useI18n();
  const [value, setValue] = useState(initial);
  // a code from the link that is not a code says so at once; a typed one on submit
  const [problem, setProblem] = useState(initial ? tvCodeProblem(initial) : null);

  function submit(e: FormEvent) {
    e.preventDefault();
    const p = tvCodeProblem(value);
    setProblem(p);
    const code = parseTvCode(value);
    if (!p && code) onCode(code);
  }

  return (
    <Card>
      <p>{t("tv.intro")}</p>
      <form className="mkc-form" onSubmit={submit} noValidate>
        <Input
          label={t("tv.code.label")}
          description={t("tv.code.hint")}
          error={problem ? t(`tv.code.${problem}`) : undefined}
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          inputMode="text"
          maxLength={12}
          dir="ltr"
          className="mkc-tv__code"
          value={value}
          onChange={(e) => {
            setValue(e.target.value);
            if (problem) setProblem(null);
          }}
        />
        <div className="mkc-row mkc-tv__actions">
          <Button variant="primary" type="submit">
            {t("tv.code.next")}
          </Button>
        </div>
      </form>
      <p className="mkc-muted">{t("tv.withoutCloud")}</p>
    </Card>
  );
}

type Phase = { kind: "ask" } | { kind: "cancelled" } | { kind: "done"; home: string };

function Confirm({ code, onOther }: { code: string; onOther: () => void }) {
  const { t, locale } = useI18n();
  const lookupMessage = useTvErrorMessage("lookup");
  const approveMessage = useTvErrorMessage("approve");
  const tv = useLoad(() => api.get<TvCode>(`/v1/tv/codes/${encodeURIComponent(code)}`), code);
  const homes = useLoad(async () => {
    const r = await api.get<{ homes: CloudHome[] }>("/v1/homes");
    if (!r || !Array.isArray(r.homes)) throw new TypeError("not the API's answer");
    return r.homes.filter((h) => h.linked);
  }, "tv-homes");
  const [chosen, setChosen] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [phase, setPhase] = useState<Phase>({ kind: "ask" });
  const heading = useRef<HTMLHeadingElement>(null);

  const linked = homes.data ?? [];
  // one linked home that can answer: it is the one
  useEffect(() => {
    if (chosen === null && linked.length === 1 && linked[0].online) setChosen(linked[0].server_id);
  }, [chosen, linked]);

  // the question, the outcome: each takes the focus as it appears, for keyboard and screen readers
  useEffect(() => {
    heading.current?.focus();
  }, [tv.data, phase.kind]);

  if (tv.loading) return <Loading />;
  if (tv.error !== null) {
    const known = tv.error instanceof CloudApiError && LOOKUP_ERRORS.has(tv.error.code);
    return (
      <Card>
        {known ? (
          <Banner tone="error">{lookupMessage(tv.error)}</Banner>
        ) : (
          <LoadError error={tv.error} onRetry={tv.reload} />
        )}
        <div className="mkc-row mkc-tv__actions">
          <Button variant="secondary" onClick={onOther}>
            {t("tv.otherCode")}
          </Button>
        </div>
      </Card>
    );
  }
  const info = tv.data;
  if (!info) return null;

  if (phase.kind === "done") {
    return (
      <Card>
        <h2 ref={heading} tabIndex={-1}>
          {t("approveTv.cloud.done")}
        </h2>
        <p>{t("tv.doneHome", { home: isolate(phase.home) })}</p>
      </Card>
    );
  }
  if (phase.kind === "cancelled") {
    return (
      <Card>
        <h2 ref={heading} tabIndex={-1}>
          {t("tv.cancelled")}
        </h2>
        <p className="mkc-muted">{t("tv.cancelledHint")}</p>
        <div className="mkc-row mkc-tv__actions">
          <Button variant="secondary" onClick={onOther}>
            {t("tv.otherCode")}
          </Button>
        </div>
      </Card>
    );
  }

  const platform = isolate(tvPlatformName(info.platform));
  const country = countryName(info.country, locale);
  const question = country
    ? t("tv.confirm", { platform, country: isolate(country) })
    : t("tv.confirmNoCountry", { platform });
  const home = linked.find((h) => h.server_id === chosen && h.online);
  const gone = error instanceof CloudApiError && CODE_GONE.has(error.code);

  async function approve() {
    if (!home) return;
    setBusy(true);
    setError(null);
    try {
      const r = await api.post<Approved>(`/v1/tv/codes/${encodeURIComponent(code)}/approve`, {
        server_id: home.server_id,
      });
      setPhase({ kind: "done", home: r?.home_name || home.name || home.slug });
    } catch (e) {
      setError(e);
      // the home's state moved under us: show the list as it is now
      if (e instanceof CloudApiError && (e.code === "home_offline" || e.code === "not_linked")) {
        homes.reload();
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <h2 ref={heading} tabIndex={-1} aria-live="polite">
        {question}
      </h2>
      <p className="mkc-muted">
        {t("tv.codeShown", { code: isolate(info.code || code) })} {t("tv.note")}
      </p>
      {homes.loading && <Loading />}
      {homes.error !== null && <LoadError error={homes.error} onRetry={homes.reload} />}
      {homes.data && linked.length === 0 && (
        <Banner tone="info">
          <p>{t("tv.noLinkedHome")}</p>
          <p>{t("tv.withoutCloud")}</p>
        </Banner>
      )}
      {linked.length > 0 && (
        <RadioGroup legend={t("tv.home.legend")} name="tv-home">
          {linked.map((h) => (
            <Radio
              key={h.server_id}
              name="tv-home"
              value={h.server_id}
              checked={chosen === h.server_id}
              disabled={!h.online || busy || gone}
              onChange={() => setChosen(h.server_id)}
              label={
                <span className="mkc-row mkc-row--tight">
                  <bdi>{h.name || h.slug}</bdi>
                  {!h.online && <Badge tone="neutral">{t("tv.home.offline")}</Badge>}
                </span>
              }
            />
          ))}
        </RadioGroup>
      )}
      {error !== null && <Banner tone="error">{approveMessage(error)}</Banner>}
      <div className="mkc-row mkc-tv__actions">
        {gone ? (
          <Button variant="secondary" onClick={onOther}>
            {t("tv.otherCode")}
          </Button>
        ) : (
          <>
            <Button
              variant="primary"
              loading={busy}
              disabled={!home}
              onClick={() => void approve()}
            >
              {t("tv.approve")}
            </Button>
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => {
                setError(null);
                setPhase({ kind: "cancelled" });
              }}
            >
              {t("tv.cancel")}
            </Button>
          </>
        )}
      </div>
    </Card>
  );
}
