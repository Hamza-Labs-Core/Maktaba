// The channel list: logo, number, name, category, what is on now (with its
// progress), how far ahead the guide reaches, where the channel comes from
// (the plan, the plan but edited by you, or made by hand), the on/off
// switch and the actions. Rows are reordered by dragging their handle, or
// from the keyboard: focus the handle and use the up / down arrows.
import { useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { apiUrl } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";
import {
  channelLogo,
  channelOrigin,
  guideHoursAhead,
  type Channel,
  type ChannelOrigin,
} from "../../../lib/channels";

/** Moves item `from` to position `to`. */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || from >= list.length) return list;
  const out = [...list];
  const [it] = out.splice(from, 1);
  out.splice(Math.max(0, Math.min(out.length, to)), 0, it);
  return out;
}

/**
 * The renumbering a new order means: the channels keep the same set of
 * numbers, handed out in the new order. Only the ones whose number changes
 * are returned.
 */
export function renumber(
  before: Channel[],
  after: Channel[]
): Array<{ id: string; number: number }> {
  const numbers = before.map((c) => c.number).sort((a, b) => a - b);
  return after
    .map((c, i) => ({ id: c.id, number: numbers[i], was: c.number }))
    .filter((x) => x.number !== x.was)
    .map(({ id, number }) => ({ id, number }));
}

const ORIGIN_TONE: Record<ChannelOrigin, "neutral" | "accent" | "warn"> = {
  plan: "accent",
  edited: "warn",
  manual: "neutral",
};

export function ChannelList({
  channels,
  nowMs,
  canReorder,
  onReorder,
  onToggle,
  onEdit,
  onDelete,
}: {
  channels: Channel[];
  nowMs: number;
  /** false while a filter hides some channels (a partial order means nothing) */
  canReorder: boolean;
  onReorder: (next: Channel[]) => void;
  onToggle: (c: Channel) => void;
  onEdit: (c: Channel, tab?: string) => void;
  onDelete: (c: Channel) => void;
}) {
  const { t, n } = useI18n();
  const [dragging, setDragging] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const [announce, setAnnounce] = useState("");
  const handles = useRef<Array<HTMLButtonElement | null>>([]);

  function drop(e: DragEvent, to: number) {
    e.preventDefault();
    if (dragging !== null && dragging !== to) onReorder(moveItem(channels, dragging, to));
    setDragging(null);
    setOver(null);
  }

  function key(e: KeyboardEvent<HTMLButtonElement>, i: number) {
    const to = e.key === "ArrowUp" ? i - 1 : e.key === "ArrowDown" ? i + 1 : null;
    if (to === null) return;
    e.preventDefault();
    if (to < 0 || to >= channels.length) return;
    onReorder(moveItem(channels, i, to));
    setAnnounce(t("channels.list.moved", { name: channels[i].name, pos: n(to + 1) }));
    // keep the focus on the moved row's handle
    requestAnimationFrame(() => handles.current[to]?.focus());
  }

  return (
    <>
      <p className="mkt-cb-sr" aria-live="polite">
        {announce}
      </p>
      <table className="mkt-table mkt-cb-list" aria-label={t("channels.title")}>
        <thead>
          <tr>
            <th aria-label={t("channels.list.order")} />
            <th>{t("channels.col.number")}</th>
            <th>{t("channels.col.name")}</th>
            <th>{t("channels.col.category")}</th>
            <th>{t("channels.col.now")}</th>
            <th>{t("channels.col.guide")}</th>
            <th>{t("channels.col.enabled")}</th>
            <th>{t("channels.col.actions")}</th>
          </tr>
        </thead>
        <tbody>
          {channels.map((c, i) => {
            const logo = channelLogo(c);
            const origin = channelOrigin(c);
            const hours = guideHoursAhead(c, nowMs);
            const np = c.now_playing;
            return (
              <tr
                key={c.id}
                className={[
                  dragging === i ? "mkt-cb-list__row--drag" : "",
                  over === i && dragging !== i ? "mkt-cb-list__row--over" : "",
                ].join(" ")}
                onDragOver={(e) => {
                  if (dragging === null) return;
                  e.preventDefault();
                  setOver(i);
                }}
                onDrop={(e) => drop(e, i)}
              >
                <td>
                  <button
                    type="button"
                    ref={(el) => {
                      handles.current[i] = el;
                    }}
                    className="mkt-cb-handle"
                    draggable={canReorder}
                    disabled={!canReorder}
                    aria-label={t("channels.list.handle", { name: c.name })}
                    title={
                      canReorder ? t("channels.list.handleHint") : t("channels.list.clearFilter")
                    }
                    onDragStart={(e) => {
                      setDragging(i);
                      e.dataTransfer.effectAllowed = "move";
                      e.dataTransfer.setData("text/plain", c.id);
                    }}
                    onDragEnd={() => {
                      setDragging(null);
                      setOver(null);
                    }}
                    onKeyDown={(e) => key(e, i)}
                  >
                    ⠿
                  </button>
                </td>
                <td className="mkt-cb-list__num">{n(c.number, { useGrouping: false })}</td>
                <td>
                  <div className="mkt-cb-list__name">
                    {logo ? (
                      <img className="mkt-cb-logo" src={apiUrl(logo)} alt="" loading="lazy" />
                    ) : (
                      <span className="mkt-cb-logo mkt-cb-logo--none" aria-hidden="true">
                        {(c.name ?? "").slice(0, 1)}
                      </span>
                    )}
                    <span>
                      <strong>{c.name}</strong>
                      <Badge tone={ORIGIN_TONE[origin]}>{t(`channels.origin.${origin}`)}</Badge>
                      <span className="mkt-muted mkt-cb-list__mode">
                        {t(`channels.mode.${c.mode}`)}
                      </span>
                    </span>
                  </div>
                </td>
                <td>{c.category || "—"}</td>
                <td className="mkt-cb-list__now">
                  {np ? (
                    <>
                      <span className="mkt-cb-list__title">
                        {np.title || t("channels.previewFiller")}
                      </span>
                      <progress
                        max={1}
                        value={np.progress}
                        aria-label={t("channels.list.progress", {
                          pct: n(Math.round(np.progress * 100)),
                        })}
                      />
                    </>
                  ) : (
                    <span className="mkt-muted">{t("channels.list.nothing")}</span>
                  )}
                </td>
                <td>
                  <span
                    className={`mkt-cb-cover${hours < 24 ? " mkt-cb-cover--low" : ""}`}
                    title={c.guide_until ?? undefined}
                  >
                    {t("channels.list.hours", { h: n(Math.floor(hours)) })}
                  </span>
                </td>
                <td>
                  <Toggle
                    checked={c.enabled}
                    onChange={() => onToggle(c)}
                    label={c.enabled ? t("channels.on") : t("channels.off")}
                    aria-label={t("channels.list.enable", { name: c.name })}
                  />
                </td>
                <td className="mkt-row-actions">
                  <Button size="sm" variant="ghost" onClick={() => onEdit(c)}>
                    {t("common.edit")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => onEdit(c, "preview")}>
                    {t("channels.preview")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => onDelete(c)}>
                    {t("common.delete")}
                  </Button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </>
  );
}
