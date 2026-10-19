// The slot bar of the Changes page (story 33.11 AC1): per host, each TV slot's role,
// state, release and version. Until the two-slot runtime (story 33.1) there is one TV
// process: the home's live slot.
import { useI18n } from "../../../lib/i18n";
import type { SlotsView, TvChange } from "../../../lib/tvChanges";
import { checksSummary } from "../../../lib/tvChanges";
import { useTk } from "./changeBits";

export function SlotBar({ view, inFlight }: { view: SlotsView | null; inFlight: TvChange | null }) {
  const { t, n } = useI18n();
  const tk = useTk();
  const hosts = new Map<string, SlotsView["slots"]>();
  for (const s of view?.slots ?? []) {
    const list = hosts.get(s.host_id) ?? [];
    list.push(s);
    hosts.set(s.host_id, list);
  }
  const progress = inFlight ? checksSummary(inFlight.verify_report) : null;
  return (
    <div className="mkt-tvx-slots" aria-label={t("tvchanges.slots.title")} role="group">
      {view === null && <span className="mkt-tvx-slot mkt-tvx-slot--skeleton" aria-hidden="true" />}
      {[...hosts.entries()].map(([host, slots]) => (
        <div key={host} className="mkt-tvx-host">
          <span className="mkt-tvx-host__name">
            {host === "home" ? t("tvchanges.slots.home") : host}
          </span>
          {slots.map((s) => (
            <span
              key={s.slot}
              className={`mkt-tvx-slot mkt-tvx-slot--${s.role}`}
              data-testid={`slot-${s.host_id}-${s.slot}`}
            >
              <strong>{tk(`tvchanges.slots.role.${s.role}`, s.role)}</strong>
              <span>{tk(`tvchanges.slots.color.${s.slot}`, s.slot)}</span>
              <span className="mkt-tvx-muted">
                {s.release_id
                  ? t("tvchanges.slots.release", { id: s.release_id.slice(0, 8) })
                  : t("tvchanges.slots.noRelease")}
              </span>
              {s.version && <span className="mkt-tvx-muted">{s.version}</span>}
              <span className="mkt-tvx-slot__state">
                {tk(`tvchanges.slots.state.${s.state}`, s.state)}
              </span>
            </span>
          ))}
          {inFlight && inFlight.state !== "draft" && (
            <span className="mkt-tvx-slot mkt-tvx-slot--staging" data-testid="slot-staging">
              <strong>{t("tvchanges.slots.role.staging")}</strong>
              <span>{t(`tvchanges.state.${inFlight.state}`)}</span>
              <span className="mkt-tvx-muted">
                {inFlight.state === "verifying" && progress
                  ? t("tvchanges.slots.checks", { done: n(progress.total), fail: n(progress.fail) })
                  : t("tvchanges.slots.change", { n: n(inFlight.number) })}
              </span>
            </span>
          )}
        </div>
      ))}
      {view?.runtime === "single" && <p className="mkt-tvx-hint">{t("tvchanges.slots.single")}</p>}
    </div>
  );
}
