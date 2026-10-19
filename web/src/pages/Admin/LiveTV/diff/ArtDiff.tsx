// The Binary & art tab (story 33.11 AC3): the streaming version from → to, the release,
// and each channel's art before (what is on the air) and after (the staged release's),
// as fixed-size thumbnails (a missing picture keeps its box: nothing moves).
import { useState } from "react";
import { Link } from "react-router-dom";
import { apiUrl } from "../../../../lib/api";
import { useI18n } from "../../../../lib/i18n";
import type { ArtChange, Release } from "../../../../lib/tvChanges";

const KINDS_FOR: Record<string, string[]> = {
  logo_path: ["logo"],
  look: ["loading", "watermark"],
  overrides: ["logo", "loading", "watermark"],
};

function Thumb({ src, alt }: { src: string; alt: string }) {
  const { t } = useI18n();
  const [bad, setBad] = useState(false);
  return (
    <span className="mkt-tvx-thumb">
      {bad ? (
        <span className="mkt-tvx-thumb__none">{t("tvchanges.art.none")}</span>
      ) : (
        <img src={src} alt={alt} loading="lazy" onError={() => setBad(true)} />
      )}
    </span>
  );
}

export function ArtDiff({
  art,
  binary,
  release,
}: {
  art?: ArtChange[];
  binary?: { from: string; to: string } | null;
  release?: Release;
}) {
  const { t, n } = useI18n();
  return (
    <div className="mkt-tvx-diff" data-testid="art-diff">
      <section className="mkt-tvx-group">
        <h3>{t("tvchanges.binary.title")}</h3>
        {binary ? (
          <p>
            <span className="mkt-tvx-del mkt-tvx-mono">{binary.from}</span> →{" "}
            <span className="mkt-tvx-add mkt-tvx-mono">{binary.to}</span>{" "}
            <Link to="/settings">{t("tvchanges.binary.notes")}</Link>
          </p>
        ) : (
          <p className="mkt-tvx-muted">
            {t("tvchanges.binary.same", { version: release?.streaming_version || "—" })}
          </p>
        )}
        {release && (
          <p className="mkt-tvx-muted">
            {t("tvchanges.binary.release", {
              id: release.id.slice(0, 8),
              gen: n(release.sched_gen),
            })}
          </p>
        )}
      </section>
      <section className="mkt-tvx-group">
        <h3>{t("tvchanges.art.title")}</h3>
        {!art || art.length === 0 ? (
          <p className="mkt-tvx-muted">{t("tvchanges.art.noChange")}</p>
        ) : (
          <ul className="mkt-tvx-art">
            {art.map((a) => {
              const kinds = [...new Set(a.what.flatMap((w) => KINDS_FOR[w] ?? []))];
              return (
                <li key={a.id}>
                  <h4>
                    {t("tvchanges.lineup.channel", { n: n(a.number ?? 0), name: a.name ?? "" })}
                  </h4>
                  {kinds.map((k) => {
                    const path = `/api/channels/${encodeURIComponent(a.id)}/art/${k}`;
                    return (
                      <div key={k} className="mkt-tvx-art__row">
                        <span className="mkt-tvx-art__kind">{t(`tvchanges.art.kind.${k}`)}</span>
                        <Thumb src={apiUrl(path)} alt={t("tvchanges.art.before")} />
                        <span aria-hidden="true">→</span>
                        {release ? (
                          <Thumb
                            src={apiUrl(`${path}?release=${encodeURIComponent(release.id)}`)}
                            alt={t("tvchanges.art.after")}
                          />
                        ) : (
                          <span className="mkt-tvx-thumb">
                            <span className="mkt-tvx-thumb__none">
                              {t("tvchanges.art.notStaged")}
                            </span>
                          </span>
                        )}
                      </div>
                    );
                  })}
                </li>
              );
            })}
          </ul>
        )}
      </section>
    </div>
  );
}
