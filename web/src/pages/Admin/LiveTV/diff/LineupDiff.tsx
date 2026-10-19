// The Lineup tab (story 33.11 AC3): added, removed, renumbered (105 → 115), renamed and
// the definition changes by field. A draft has no computed diff yet: what it holds.
import type { ReactNode } from "react";
import { useI18n } from "../../../../lib/i18n";
import { useTk } from "../changeBits";
import type { ChannelRef, LineupDiff as Lineup, PendingSummary } from "../../../../lib/tvChanges";

function chan(t: (k: string, v?: Record<string, string | number>) => string, c: ChannelRef) {
  if (c.number != null && c.name)
    return t("tvchanges.lineup.channel", { n: c.number, name: c.name });
  return c.name || c.id.slice(0, 8);
}

function Group({ title, empty, children }: { title: string; empty: boolean; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <section className="mkt-tvx-group">
      <h3>{title}</h3>
      {empty ? <p className="mkt-tvx-muted">{t("tvchanges.none")}</p> : <ul>{children}</ul>}
    </section>
  );
}

export function LineupDiff({
  lineup,
  pending,
  names,
}: {
  lineup?: Lineup;
  pending?: PendingSummary;
  /** channel id → "101 Quran" for ids the diff names without a name */
  names: Record<string, string>;
}) {
  const { t, n } = useI18n();
  const tk = useTk();
  if (!lineup) {
    if (!pending) return <p className="mkt-tvx-muted">{t("tvchanges.lineup.notYet")}</p>;
    // the server may send null for an empty group
    const channels = pending.channels ?? [];
    const settings = pending.settings ?? [];
    return (
      <div className="mkt-tvx-diff" data-testid="pending-summary">
        <p className="mkt-tvx-hint">{t("tvchanges.lineup.draftNote")}</p>
        <Group title={t("tvchanges.lineup.draftChannels")} empty={channels.length === 0}>
          {channels.map((c) => (
            <li key={c.id}>
              <span className={`mkt-tvx-op mkt-tvx-op--${c.op}`}>
                {tk(`tvchanges.op.${c.op}`, c.op)}
              </span>{" "}
              {c.number
                ? t("tvchanges.lineup.channel", { n: n(c.number), name: c.name ?? "" })
                : c.name || c.id.slice(0, 8)}
            </li>
          ))}
        </Group>
        <Group title={t("tvchanges.lineup.draftSettings")} empty={settings.length === 0}>
          {settings.map((s) => (
            <li key={s}>{t(`tvchanges.settings.section.${s}`)}</li>
          ))}
        </Group>
      </div>
    );
  }
  const name = (id: string) => names[id] ?? id.slice(0, 8);
  const added = lineup.added ?? [];
  const removed = lineup.removed ?? [];
  const renumbered = lineup.renumbered ?? [];
  const renamed = lineup.renamed ?? [];
  const changed = lineup.changed ?? [];
  return (
    <div className="mkt-tvx-diff" data-testid="lineup-diff">
      <Group title={t("tvchanges.lineup.added")} empty={added.length === 0}>
        {added.map((c) => (
          <li key={c.id} className="mkt-tvx-add">
            + {chan(t, c)}
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.removed")} empty={removed.length === 0}>
        {removed.map((c) => (
          <li key={c.id} className="mkt-tvx-del">
            − {chan(t, c)}
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.renumbered")} empty={renumbered.length === 0}>
        {renumbered.map((r) => (
          <li key={r.id}>
            {name(r.id)}:{" "}
            <span className="mkt-tvx-num" dir="ltr">
              {n(r.from)} → {n(r.to)}
            </span>
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.renamed")} empty={renamed.length === 0}>
        {renamed.map((r) => (
          <li key={r.id}>
            <span className="mkt-tvx-del">{r.from}</span> →{" "}
            <span className="mkt-tvx-add">{r.to}</span>
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.changed")} empty={changed.length === 0}>
        {changed.map((c) => (
          <li key={c.id}>
            {chan(t, c)}:{" "}
            {(c.fields ?? []).map((f) => (
              <span key={f} className="mkt-tvx-kind">
                {tk(`tvchanges.field.${f}`, f)}
              </span>
            ))}
          </li>
        ))}
      </Group>
    </div>
  );
}
