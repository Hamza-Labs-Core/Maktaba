// Epic 57 story 57.08 on the web: what a restricted account did, for its guardians
// (Family > the account > Activity).
//
//   GET /api/users/{id}/parental/activity?kind=&cursor=   {events, next_cursor?}
//
// Newest first, fifty a page, kept ninety days by the home. An event names what it
// is about (a subject in a library this reader cannot open comes with its kind
// only), the guardian who answered, the device of a signal, or, for the time
// events, minutes and no subject.
import { useEffect, useState } from "react";
import { useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Select } from "@ds/components/Select/Select";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ApiError } from "../../lib/api";
import { useI18n, type I18nState } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import {
  EVENT_KINDS,
  fetchActivity,
  type ActivityEvent,
  type ActivityPage as Page,
  type EventKind,
} from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { BackButton } from "../../components/BackButton";
import { TitleRow } from "../../help/HelpLink";
import { timeWords } from "../../components/parental/TimeState";
import { childPath } from "./Child";
import "../../styles/parental.css";

/** What an event is about, in words; "" when it is about nothing. */
export function eventDetail(
  e: ActivityEvent,
  words: Pick<I18nState, "t" | "n" | "locale">
): string {
  const { t } = words;
  const parts: string[] = [];
  if (e.minutes !== null) parts.push(timeWords(e.minutes * 60, words));
  const s = e.subject;
  if (s && s.kind !== "time") {
    if (s.removed) parts.push(t("family.subject.removed"));
    else if (s.hidden) parts.push(t("family.subject.hidden"));
    else if (s.title) parts.push(s.title);
  }
  if (e.device) parts.push(e.device);
  if (e.guardian) parts.push(e.guardian.username || e.guardian.id);
  return parts.join(" · ");
}

export function ActivityPage() {
  const { userId = "" } = useParams();
  const words = useI18n();
  const { t, formatDate } = words;
  const name = useMyParental().children.find((c) => c.id === userId)?.username ?? "";
  const [kind, setKind] = useState<EventKind | "">("");
  const { data, err, loading, reload } = usePageData<Page>(
    userId ? `parental:activity:${userId}:${kind}` : null,
    () => fetchActivity(userId, kind),
    [userId, kind]
  );
  // the pages after the first, read on Load more
  const [more, setMore] = useState<ActivityEvent[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  useEffect(() => {
    setMore([]);
    setCursor(data?.nextCursor ?? null);
    setMoreFailed(false);
  }, [data]);

  async function loadMore() {
    if (!cursor) return;
    setLoadingMore(true);
    setMoreFailed(false);
    try {
      const page = await fetchActivity(userId, kind, cursor);
      setMore((m) => [...m, ...page.events]);
      setCursor(page.nextCursor);
    } catch {
      setMoreFailed(true);
    } finally {
      setLoadingMore(false);
    }
  }

  const back = childPath(userId);
  const events = data ? [...data.events, ...more] : [];
  return (
    <section className="mkt-page">
      <BackButton fallback={back} />
      <TitleRow>
        <h1>
          {t("family.child.activity")}
          {name && (
            <>
              : <bdi>{name}</bdi>
            </>
          )}
        </h1>
      </TitleRow>
      <p className="mkt-parental-note">{t("family.activity.intro")}</p>
      <Select
        label={t("family.activity.filter")}
        value={kind}
        onChange={(e) => setKind(e.target.value as EventKind | "")}
      >
        <option value="">{t("family.activity.all")}</option>
        {EVENT_KINDS.map((k) => (
          <option key={k} value={k}>
            {t(`family.activity.kind.${k}`)}
          </option>
        ))}
      </Select>
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && (
        <LoadError
          error={err}
          onRetry={reload}
          backTo="/family"
          title={
            err instanceof ApiError && err.status === 404 ? t("family.error.notFound") : undefined
          }
        />
      )}
      {data && events.length === 0 && (
        <EmptyState
          kind="cleared"
          title={t("family.activity.empty.title")}
          description={t("family.activity.empty.desc")}
        />
      )}
      {events.length > 0 && (
        <table className="mkt-parental-usage" data-testid="family-activity">
          <thead>
            <tr>
              <th scope="col">{t("family.activity.col.when")}</th>
              <th scope="col">{t("family.activity.col.what")}</th>
              <th scope="col">{t("family.activity.col.detail")}</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.id} data-kind={e.kind}>
                <td>{formatDate(e.at)}</td>
                <td>
                  <Badge
                    tone={e.kind === "blocked" || e.kind === "pin_locked" ? "warn" : "neutral"}
                  >
                    {t(e.kind ? `family.activity.kind.${e.kind}` : "family.activity.kind.other")}
                  </Badge>
                </td>
                <td>
                  <bdi>{eventDetail(e, words)}</bdi>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {moreFailed && (
        <p className="mkt-alert" role="alert">
          {t("common.error")}
        </p>
      )}
      {cursor && (
        <div className="mkt-parental-actions">
          <Button variant="secondary" loading={loadingMore} onClick={() => void loadMore()}>
            {t("common.loadMore")}
          </Button>
        </div>
      )}
    </section>
  );
}
