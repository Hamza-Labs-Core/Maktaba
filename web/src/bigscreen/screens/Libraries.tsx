// Epic 46 — big screen's Libraries: a card per library the user may see
// (GET /api/libraries/summary). A library itself, with its tabs, is
// screens/Library.tsx (Epic 58 story 58.08).
import { useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibrarySummary } from "../../lib/browse";
import { bigPath, useBigNav } from "../nav";
import { Empty, Failed, Grid, Loading, Picture, Screen, posterSmall } from "../ui";
import { errorText } from "../../lib/errorText";

export function BigLibraries() {
  const { t, n } = useI18n();
  const { go } = useBigNav();
  const [libs, setLibs] = useState<LibrarySummary[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let cancelled = false;
    setErr(null);
    fetchLibrarySummary()
      .then((l) => !cancelled && setLibs(l))
      .catch((e) => !cancelled && setErr(errorText(e, t)));
    return () => {
      cancelled = true;
    };
  }, [reload, t]);

  if (err) return <Failed message={err} onRetry={() => setReload((x) => x + 1)} />;
  if (!libs) return <Loading />;
  // pinned first, in the user's order, then by name
  const sorted = [...libs].sort((a, b) =>
    a.pinned !== b.pinned
      ? a.pinned
        ? -1
        : 1
      : a.pinned
        ? (a.pin_position ?? 0) - (b.pin_position ?? 0)
        : a.name.localeCompare(b.name)
  );
  return (
    <Screen title={t("big.tab.libraries")} testId="big-libraries">
      {sorted.length === 0 ? (
        <Empty>{t("big.libraries.empty")}</Empty>
      ) : (
        <Grid id="libraries" className="bs-grid--libraries">
          {sorted.map((l, i) => (
            <button
              key={l.id}
              type="button"
              role="listitem"
              className="bs-tile bs-tile--library"
              data-bs-focus={`lib:${l.id}`}
              data-bs-autofocus={i === 0 || undefined}
              onClick={() => go(bigPath.library(l.id))}
            >
              <span className="bs-tile__art">
                <Picture src={l.poster_video_ids[0] ? posterSmall(l.poster_video_ids[0]) : null} />
                {l.pinned && (
                  <span className="bs-tile__pin" aria-label={t("big.pinned")}>
                    ★
                  </span>
                )}
              </span>
              <bdi className="bs-tile__title">{l.name}</bdi>
              <span className="bs-tile__meta">{t("big.videoCount", { n: n(l.videos) })}</span>
            </button>
          ))}
        </Grid>
      )}
    </Screen>
  );
}
