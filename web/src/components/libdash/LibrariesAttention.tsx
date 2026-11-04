// The Queues page's libraries panel: how many libraries need an admin, the
// most urgent few with why (failures, a YouTube source that did not list,
// duplicates to review, files to convert), each opening its library page at
// the queues, and the way to the library dashboard, where every library has
// its tile and the bulk actions are. A non-admin (the page shows them the
// queues too) gets each library's queue items instead, and no dashboard link.
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import type { LibraryQueueRow, OfflineSummary } from "../../lib/queueApi";
import { libraryHealth, sortLibraries, type LibraryHealth, type Reason } from "./health";
import { libraryPath } from "./LibraryTile";

export const ATTENTION_SHOWN = 6;

function ReasonBadge({ h, r }: { h: LibraryHealth; r: Reason }) {
  const { t, n } = useI18n();
  switch (r) {
    case "failing":
      return <Badge tone="error">{t("libdash.n.failed", { n: n(h.failed) })}</Badge>;
    case "youtube":
      return <Badge tone="error">{t("libdash.yt.problem")}</Badge>;
    case "duplicates":
      return <Badge tone="accent">{t("libdash.duplicates", { n: n(h.duplicates) })}</Badge>;
    default:
      return <Badge tone="warn">{t("libdash.convert", { n: n(h.toConvert) })}</Badge>;
  }
}

export function LibrariesAttention({
  rows,
  offline,
  admin = true,
}: {
  rows: LibraryQueueRow[];
  offline?: Record<string, OfflineSummary>;
  admin?: boolean;
}) {
  const { t, n, locale } = useI18n();
  const all = rows.map((r) => libraryHealth(r, offline?.[r.library_id]));
  const needing = sortLibraries(
    all.filter((h) => h.reasons.length > 0),
    "attention",
    locale
  );
  const target = (id: string) =>
    admin ? libraryPath(id, "queues") : `/queue/items/library/${encodeURIComponent(id)}`;
  return (
    <section className="mkt-q-panel mkt-ld-attn" aria-labelledby="mkt-ld-attn-h">
      <div className="mkt-q-section-head">
        <h2 className="mkt-q-section-title" id="mkt-ld-attn-h">
          {t("queue.libs.title")}
        </h2>
        {admin && (
          <Link className="mkt-ld-attn__open" to="/queue/libraries">
            {t("libdash.open", { n: n(all.length) })}
          </Link>
        )}
      </div>
      {all.length === 0 ? (
        <p className="mkt-q-empty">{t("libdash.empty.none")}</p>
      ) : needing.length === 0 ? (
        <p className="mkt-q-empty">{t("libdash.attn.none", { n: n(all.length) })}</p>
      ) : (
        <>
          <p className="mkt-muted">
            {t("libdash.attn.count", { n: n(needing.length), total: n(all.length) })}
          </p>
          <ul className="mkt-ld-attn__list">
            {needing.slice(0, ATTENTION_SHOWN).map((h) => (
              <li key={h.id}>
                <Link className="mkt-ld-attn__name" to={target(h.id)}>
                  {bidiIsolate(h.name)}
                </Link>
                <span className="mkt-ld-attn__why">
                  {h.reasons.map((r) => (
                    <ReasonBadge key={r} h={h} r={r} />
                  ))}
                </span>
              </li>
            ))}
          </ul>
          {admin && needing.length > ATTENTION_SHOWN && (
            <Link to="/queue/libraries?show=attention">
              {t("libdash.attn.more", { n: n(needing.length - ATTENTION_SHOWN) })}
            </Link>
          )}
        </>
      )}
    </section>
  );
}
