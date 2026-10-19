// "Topics, clips and promos" of one library (the stand-alone item page's second
// table group): the topic collections, the clip and stand-alone groups and the
// promos linked to their shows, each with the Plex collection written for it.
// GET /api/items/library/{id}/collections.
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { LibraryCollections as Data } from "../../lib/queueApi";

export function LibraryCollections({ libraryId }: { libraryId: string }) {
  const { t, n } = useI18n();
  const [data, setData] = useState<Data | null>(null);

  useEffect(() => {
    let alive = true;
    setData(null);
    queueApi
      .libraryCollections(libraryId)
      .then(
        (d) =>
          alive &&
          setData({
            ...d,
            topics: d?.topics ?? [],
            clip_groups: d?.clip_groups ?? [],
            promos: d?.promos ?? [],
          })
      )
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [libraryId]);

  if (!data || data.topics.length + data.clip_groups.length + data.promos.length === 0) return null;
  const plex = (c: string | null) =>
    c ? bidiIsolate(c) : <span className="mkt-q-muted">{t("queue.coll.notWritten")}</span>;

  return (
    <section className="mkt-q-panel" aria-labelledby="mkt-q-coll-h">
      <h2 className="mkt-q-section-title" id="mkt-q-coll-h">
        {t("queue.coll.title")}
      </h2>
      {data.topics.length > 0 && (
        <div className="mkt-q-tablewrap">
          <table className="mkt-q-table">
            <caption>{t("queue.coll.topics", { n: n(data.topics.length) })}</caption>
            <thead>
              <tr>
                <th scope="col">{t("queue.coll.name")}</th>
                <th scope="col">{t("queue.coll.items")}</th>
                <th scope="col">{t("queue.coll.plex")}</th>
              </tr>
            </thead>
            <tbody>
              {data.topics.map((x) => (
                <tr key={x.id}>
                  <td dir="auto">{bidiIsolate(x.name)}</td>
                  <td>{n(x.items)}</td>
                  <td dir="auto">{plex(x.collection)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data.clip_groups.length > 0 && (
        <div className="mkt-q-tablewrap">
          <table className="mkt-q-table">
            <caption>{t("queue.coll.clips", { n: n(data.clip_groups.length) })}</caption>
            <thead>
              <tr>
                <th scope="col">{t("queue.coll.name")}</th>
                <th scope="col">{t("queue.coll.kind")}</th>
                <th scope="col">{t("queue.coll.items")}</th>
                <th scope="col">{t("queue.coll.plex")}</th>
              </tr>
            </thead>
            <tbody>
              {data.clip_groups.map((x) => (
                <tr key={x.id}>
                  <td dir="auto">{bidiIsolate(x.title)}</td>
                  <td>
                    {t(`queue.coll.kind.${x.kind}`)}{" "}
                    <span className="mkt-q-muted">· {t(`queue.coll.basis.${x.basis}`)}</span>
                  </td>
                  <td>{n(x.items)}</td>
                  <td dir="auto">{plex(x.collection)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {data.promos.length > 0 && (
        <div className="mkt-q-tablewrap">
          <table className="mkt-q-table">
            <caption>{t("queue.coll.promos", { n: n(data.promos.length) })}</caption>
            <thead>
              <tr>
                <th scope="col">{t("queue.coll.promo")}</th>
                <th scope="col">{t("queue.coll.show")}</th>
                <th scope="col">{t("queue.coll.evidence")}</th>
              </tr>
            </thead>
            <tbody>
              {data.promos.map((x) => (
                <tr key={x.video_id}>
                  <td dir="auto">
                    <Link to={`/videos/${encodeURIComponent(x.video_id)}`}>
                      {bidiIsolate(x.title)}
                    </Link>
                  </td>
                  <td dir="auto">
                    {x.season_id ? (
                      <Link to={`/queue/items/season/${encodeURIComponent(x.season_id)}`}>
                        {bidiIsolate(x.show)}
                        {x.season !== null && ` S${n(x.season)}`}
                      </Link>
                    ) : (
                      bidiIsolate(x.show)
                    )}
                  </td>
                  <td className="mkt-q-muted">{x.evidence}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
