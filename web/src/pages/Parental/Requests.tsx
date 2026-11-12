// Epic 57 family settings on the web: the requests that wait for an answer
// (Family > Requests).
//
//   GET  /api/parental/requests?status=pending   an admin: every child's; a guardian: their own
//   POST /api/parental/requests/{id}/approve     {expires_at?} for one thing; {minutes?} for `time`
//   POST /api/parental/requests/{id}/deny
//
// Approving one thing adds an allow rule (until today's end, for a week, or with no
// end); approving a request for more time gives minutes for today. The home answers
// 409 when someone else answered first and 410 when it expired: the list reads again.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Select } from "@ds/components/Select/Select";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { apiUrl } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import { MAX_GRANT_MINUTES, tierKey } from "../../lib/parentalApi";
import {
  approveRequest,
  denyRequest,
  fetchRequests,
  refusalCode,
  type RequestItem,
} from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { BackButton } from "../../components/BackButton";
import { TitleRow } from "../../help/HelpLink";
import { timeWords } from "../../components/parental/TimeState";
import { ExpirySelect, expiryOf, type ExpiryChoice } from "./subjects";
import { familyError } from "./words";
import "../../styles/parental.css";

/** The minutes an answer may give, beside the minutes asked for. */
export const GIVE_MINUTES = [15, 30, 60, 120] as const;

export function RequestsPage() {
  const { t } = useI18n();
  const parental = useMyParental();
  const { data, err, loading, reload, setData } = usePageData<RequestItem[]>(
    "parental:requests",
    () => fetchRequests("pending")
  );
  const answered = (id: string) => {
    setData((prev) => (prev ?? []).filter((r) => r.id !== id));
    parental.refresh();
  };
  return (
    <section className="mkt-page">
      <BackButton fallback="/family" />
      <TitleRow>
        <h1>{t("family.requests")}</h1>
      </TitleRow>
      <p className="mkt-parental-note">{t("family.requests.intro")}</p>
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} backTo="/family" />}
      {data && data.length === 0 && (
        <EmptyState
          kind="cleared"
          title={t("family.requests.empty.title")}
          description={t("family.requests.empty.desc")}
        />
      )}
      {data && data.length > 0 && (
        <ul className="mkt-parental-list" data-testid="family-requests">
          {data.map((r) => (
            <RequestRow key={r.id} item={r} onAnswered={() => answered(r.id)} onGone={reload} />
          ))}
        </ul>
      )}
    </section>
  );
}

function RequestRow({
  item,
  onAnswered,
  onGone,
}: {
  item: RequestItem;
  onAnswered: () => void;
  onGone: () => void;
}) {
  const words = useI18n();
  const { t, formatDate } = words;
  const toast = useToast();
  const isTime = item.subjectKind === "time";
  const [expiry, setExpiry] = useState<ExpiryChoice>("today");
  const [minutes, setMinutes] = useState<number>(item.minutes || GIVE_MINUTES[1]);
  const [busy, setBusy] = useState<"approve" | "deny" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const choices = [...new Set([item.minutes, ...GIVE_MINUTES])]
    .filter((m) => m >= 1 && m <= MAX_GRANT_MINUTES)
    .sort((a, b) => a - b);

  async function answer(which: "approve" | "deny") {
    setBusy(which);
    setError(null);
    try {
      if (which === "deny") {
        await denyRequest(item.id);
        toast.show({ tone: "success", message: t("family.requests.denied") });
      } else if (isTime) {
        await approveRequest(item.id, { minutes });
        toast.show({
          tone: "success",
          message: t("parental.time.granted", { time: timeWords(minutes * 60, words) }),
        });
      } else {
        await approveRequest(item.id, expiryOf(expiry));
        toast.show({ tone: "success", message: t("family.requests.approved") });
      }
      onAnswered();
    } catch (e) {
      const code = refusalCode(e);
      if (code === "request_decided" || code === "request_expired") {
        // answered by someone else, or gone: the list says what waits now
        toast.show({ tone: "warn", message: familyError(e, t) });
        onGone();
      } else {
        setError(familyError(e, t));
      }
    } finally {
      setBusy(null);
    }
  }

  const s = item.subject;
  const title = isTime
    ? t("family.requests.time", { time: timeWords(item.minutes * 60, words) })
    : s.removed
      ? t("family.subject.removed")
      : s.hidden
        ? t("family.subject.hidden")
        : s.title || t(`family.kind.${item.subjectKind}`);
  return (
    <li data-testid="family-request" data-kind={item.subjectKind}>
      {s.posterUrl && !isTime && (
        <img className="mkt-parental-poster" src={apiUrl(s.posterUrl)} alt="" loading="lazy" />
      )}
      <div className="mkt-parental-list__main">
        <strong>
          <bdi>{title}</bdi>
        </strong>
        <span>
          {!isTime && <Badge tone="neutral">{t(`family.kind.${item.subjectKind}`)}</Badge>}{" "}
          {!isTime && s.tier !== null && <Badge tone="neutral">{t(tierKey(s.tier))}</Badge>}
        </span>
        <span className="mkt-muted">
          {t("family.requests.by", {
            name: item.child.username || item.child.id,
            when: formatDate(item.createdAt),
          })}
        </span>
        {error && (
          <p className="mkt-alert" role="alert">
            {error}
          </p>
        )}
      </div>
      <div className="mkt-parental-row">
        {isTime ? (
          <Select
            label={t("family.requests.give")}
            value={String(minutes)}
            onChange={(e) => setMinutes(Number(e.target.value))}
          >
            {choices.map((m) => (
              <option key={m} value={m}>
                {timeWords(m * 60, words)}
              </option>
            ))}
          </Select>
        ) : (
          <ExpirySelect value={expiry} onChange={setExpiry} label={t("family.requests.for")} />
        )}
        <Button
          disabled={busy !== null}
          loading={busy === "approve"}
          onClick={() => void answer("approve")}
        >
          {t("family.device.approve")}
        </Button>
        <Button
          variant="secondary"
          disabled={busy !== null}
          loading={busy === "deny"}
          onClick={() => void answer("deny")}
        >
          {t("family.deny")}
        </Button>
      </div>
    </li>
  );
}
