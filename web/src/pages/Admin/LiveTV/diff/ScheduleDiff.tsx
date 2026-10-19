// The Schedules tab (story 33.11 AC3, EC1): per regenerated channel the divergence
// time, and on opening it the next 24 h before / after side by side; a channel whose
// programmes did not change is marked "unchanged" and stays folded. Channels come ten
// a page from the server (plan 33.11 D3).
import { useEffect, useState } from "react";
import { useI18n } from "../../../../lib/i18n";
import {
  tvChangesApi,
  type Programme,
  type ScheduleChannelDiff,
  type SchedulePage,
} from "../../../../lib/tvChanges";
import { Clock, actionError } from "../changeBits";

export function ScheduleDiff({ changeId, staged }: { changeId: string; staged: boolean }) {
  const { t, n } = useI18n();
  const [page, setPage] = useState(1);
  const [data, setData] = useState<SchedulePage | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<Record<string, ScheduleChannelDiff | "loading" | "error">>({});

  useEffect(() => {
    let alive = true;
    tvChangesApi
      .scheduleChannels(changeId, page)
      .then((d) => alive && (setData(d), setError(null)))
      .catch((e) => alive && setError(actionError(t, e)));
    return () => {
      alive = false;
    };
  }, [changeId, page, t]);

  async function toggle(id: string) {
    if (open[id]) {
      setOpen(({ [id]: _drop, ...rest }) => rest);
      return;
    }
    setOpen((o) => ({ ...o, [id]: "loading" }));
    try {
      const d = await tvChangesApi.scheduleChannel(changeId, id);
      setOpen((o) => ({ ...o, [id]: d }));
    } catch {
      setOpen((o) => ({ ...o, [id]: "error" }));
    }
  }

  if (!staged) return <p className="mkt-tvx-muted">{t("tvchanges.schedules.notYet")}</p>;
  if (error) return <p role="alert">{error}</p>;
  if (!data) return <div className="mkt-tvx-skeleton" aria-hidden="true" />;
  if (data.total === 0) return <p className="mkt-tvx-muted">{t("tvchanges.schedules.none")}</p>;

  return (
    <div className="mkt-tvx-diff" data-testid="schedule-diff">
      <p className="mkt-tvx-hint">{t("tvchanges.schedules.intro", { n: n(data.total) })}</p>
      <ul className="mkt-tvx-sched">
        {(data.channels ?? []).map((c) => {
          const o = open[c.id];
          return (
            <li key={c.id}>
              <button
                type="button"
                className="mkt-tvx-sched__head"
                aria-expanded={!!o}
                onClick={() => void toggle(c.id)}
              >
                <span className="mkt-tvx-num">{n(c.number ?? 0)}</span>
                <span className="mkt-tvx-sched__name">{c.name}</span>
                <span className="mkt-tvx-muted">
                  {c.reason === "new"
                    ? t("tvchanges.schedules.new")
                    : t("tvchanges.schedules.diverges")}{" "}
                  {c.divergence_at && <Clock at={c.divergence_at} />}
                </span>
                {o && o !== "loading" && o !== "error" && o.unchanged && (
                  <span className="mkt-tvx-chip mkt-tvx-chip--neutral">
                    {t("tvchanges.schedules.unchanged")}
                  </span>
                )}
              </button>
              {o === "loading" && (
                <div className="mkt-tvx-skeleton mkt-tvx-skeleton--short" aria-hidden="true" />
              )}
              {o === "error" && <p role="alert">{t("tvchanges.schedules.loadError")}</p>}
              {o && o !== "loading" && o !== "error" && !o.unchanged && <SideBySide d={o} />}
            </li>
          );
        })}
      </ul>
      {data.pages > 1 && (
        <nav className="mkt-tvx-pager" aria-label={t("tvchanges.schedules.pages")}>
          <button
            type="button"
            className="mkt-btn mkt-btn--ghost"
            disabled={page <= 1}
            onClick={() => setPage(page - 1)}
          >
            {t("tvchanges.prev")}
          </button>
          <span>{t("tvchanges.pageOf", { page: n(page), pages: n(data.pages) })}</span>
          <button
            type="button"
            className="mkt-btn mkt-btn--ghost"
            disabled={page >= data.pages}
            onClick={() => setPage(page + 1)}
          >
            {t("tvchanges.next")}
          </button>
        </nav>
      )}
    </div>
  );
}

function SideBySide({ d }: { d: ScheduleChannelDiff }) {
  const { t } = useI18n();
  const keyOf = (p: Programme) => `${p.start_at}|${p.end_at}|${p.title}`;
  const before = d.before ?? [];
  const after = d.after ?? [];
  const beforeKeys = new Set(before.map(keyOf));
  const afterKeys = new Set(after.map(keyOf));
  const col = (rows: Programme[], other: Set<string>, side: "before" | "after") => (
    <ol className="mkt-tvx-progs" aria-label={t(`tvchanges.schedules.${side}`)}>
      {rows.length === 0 && <li className="mkt-tvx-muted">{t("tvchanges.schedules.empty")}</li>}
      {rows.map((p, i) => (
        <li
          key={`${p.start_at}-${i}`}
          className={other.has(keyOf(p)) ? "" : side === "before" ? "mkt-tvx-del" : "mkt-tvx-add"}
        >
          <Clock at={p.start_at} />{" "}
          <span>{p.title || t(`tvchanges.schedules.kind.${p.kind}`)}</span>
        </li>
      ))}
    </ol>
  );
  return (
    <div className="mkt-tvx-sbs">
      <div>
        <h4>{t("tvchanges.schedules.before")}</h4>
        {col(before, afterKeys, "before")}
      </div>
      <div>
        <h4>{t("tvchanges.schedules.after")}</h4>
        {col(after, beforeKeys, "after")}
      </div>
    </div>
  );
}
