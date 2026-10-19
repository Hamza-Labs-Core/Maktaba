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
    return (
      <div className="mkt-tvx-diff" data-testid="pending-summary">
        <p className="mkt-tvx-hint">{t("tvchanges.lineup.draftNote")}</p>
        <Group title={t("tvchanges.lineup.draftChannels")} empty={pending.channels.length === 0}>
          {pending.channels.map((c) => (
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
        <Group title={t("tvchanges.lineup.draftSettings")} empty={pending.settings.length === 0}>
          {pending.settings.map((s) => (
            <li key={s}>{t(`tvchanges.settings.section.${s}`)}</li>
          ))}
        </Group>
      </div>
    );
  }
  const name = (id: string) => names[id] ?? id.slice(0, 8);
  return (
    <div className="mkt-tvx-diff" data-testid="lineup-diff">
      <Group title={t("tvchanges.lineup.added")} empty={lineup.added.length === 0}>
        {lineup.added.map((c) => (
          <li key={c.id} className="mkt-tvx-add">
            + {chan(t, c)}
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.removed")} empty={lineup.removed.length === 0}>
        {lineup.removed.map((c) => (
          <li key={c.id} className="mkt-tvx-del">
            − {chan(t, c)}
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.renumbered")} empty={lineup.renumbered.length === 0}>
        {lineup.renumbered.map((r) => (
          <li key={r.id}>
            {name(r.id)}:{" "}
            <span className="mkt-tvx-num" dir="ltr">
              {n(r.from)} → {n(r.to)}
            </span>
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.renamed")} empty={lineup.renamed.length === 0}>
        {lineup.renamed.map((r) => (
          <li key={r.id}>
            <span className="mkt-tvx-del">{r.from}</span> →{" "}
            <span className="mkt-tvx-add">{r.to}</span>
          </li>
        ))}
      </Group>
      <Group title={t("tvchanges.lineup.changed")} empty={lineup.changed.length === 0}>
        {lineup.changed.map((c) => (
          <li key={c.id}>
            {chan(t, c)}:{" "}
            {c.fields.map((f) => (
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
