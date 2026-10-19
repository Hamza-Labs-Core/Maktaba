// The next 48 hours of the channel's generated guide, by day, filler
// collapsed into one line per gap, and the "Regenerate schedule" action
// (POST /api/channels/{id}/regenerate: the scheduler rebuilds the future
// from the next programme boundary).
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import { channelsApi, isFiller, type Program } from "../../../lib/channels";

export const PREVIEW_HOURS = 48;

interface Row {
  start: string;
  end: string;
  title: string;
  episode?: string | null;
  filler: boolean;
}

/** Consecutive filler blocks become one row. */
export function collapseFiller(programs: Program[]): Row[] {
  const out: Row[] = [];
  for (const p of programs) {
    const filler = isFiller(p.kind);
    const last = out[out.length - 1];
    if (filler && last?.filler) {
      last.end = p.end_at;
      continue;
    }
    out.push({ start: p.start_at, end: p.end_at, title: p.title, episode: p.episode, filler });
  }
  return out;
}

export function PreviewSection({ channelId }: { channelId: string }) {
  const { t, locale } = useI18n();
  const toast = useToast();
  const [programs, setPrograms] = useState<Program[] | null>(null);
  const [busy, setBusy] = useState(false);

  const load = useCallback(() => {
    setPrograms(null);
    channelsApi
      .schedulePreview(channelId, PREVIEW_HOURS)
      .then((r) => setPrograms(r.channels[0]?.programs ?? []))
      .catch(() => setPrograms([]));
  }, [channelId]);
  useEffect(load, [load]);

  async function regenerate() {
    if (!window.confirm(t("channels.preview.regenConfirm"))) return;
    setBusy(true);
    try {
      await channelsApi.regenerate(channelId);
      toast.show({ tone: "success", message: t("channels.preview.regenQueued") });
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.title : t("common.error"),
      });
    } finally {
      setBusy(false);
    }
  }

  const lang = locale === "ar" ? "ar-EG" : "en";
  const time = (s: string) =>
    new Date(s).toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" });
  const day = (s: string) =>
    new Date(s).toLocaleDateString(lang, { weekday: "long", day: "numeric", month: "short" });

  const rows = programs ? collapseFiller(programs) : [];
  const groups: Array<{ day: string; rows: Row[] }> = [];
  for (const r of rows) {
    const d = day(r.start);
    if (groups[groups.length - 1]?.day !== d) groups.push({ day: d, rows: [] });
    groups[groups.length - 1].rows.push(r);
  }

  return (
    <div className="mkt-cb-preview">
      <div className="mkt-cb-toolbar">
        <p className="mkt-muted">{t("channels.preview.note", { h: PREVIEW_HOURS })}</p>
        <Button size="sm" variant="ghost" onClick={load}>
          {t("channels.preview.refresh")}
        </Button>
        <Button size="sm" variant="secondary" onClick={regenerate} disabled={busy}>
          {t("channels.preview.regenerate")}
        </Button>
      </div>
      {programs === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : rows.length === 0 ? (
        <p className="mkt-muted">{t("channels.previewEmpty")}</p>
      ) : (
        groups.map((g) => (
          <section key={g.day} className="mkt-cb-preview__day">
            <h4>{g.day}</h4>
            <ol className="mkt-preview">
              {g.rows.map((r) => (
                <li key={r.start} className={r.filler ? "mkt-preview__filler" : ""}>
                  <span className="mkt-preview__time">
                    {time(r.start)}–{time(r.end)}
                  </span>
                  <span className="mkt-preview__title">
                    {r.filler ? t("channels.previewFiller") : r.title}
                    {!r.filler && r.episode ? (
                      <span className="mkt-muted"> · {r.episode}</span>
                    ) : null}
                  </span>
                </li>
              ))}
            </ol>
          </section>
        ))
      )}
    </div>
  );
}
