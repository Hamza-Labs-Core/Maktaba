// /queue/duplicates (Epic 47): the duplicate groups the checks found. Per group
// its members with path, size, resolution, codec and watch data; choose the
// keeper, Apply (queues the merge: the keeper keeps everything attached to the
// others, the other files go to the trash for 30 days) or Dismiss (never shown
// again). Exact groups in one library merge by themselves; Apply all exact
// approves the ones still open here. The API enforces admin.
import { useCallback, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { DuplicateGroup, DuplicateMember } from "../../lib/queueApi";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { useFmt } from "../../components/queue/format";
import { useBytes } from "../../components/queue/offline";
import { useAction } from "../../components/queue/useAction";
import { TitleRow } from "../../help/HelpLink";
import "../../styles/queue.css";

const STATUSES = ["open", "failed", "applied", "dismissed", "all"] as const;
const LIVE = new Set(["pending", "claimed", "running", "resuming", "paused"]);

/** A group's suggested keeper when none is recorded: the best picture, then the most
 * watched, then the largest file (a byte-identical copy is never a keeper of its own). */
export function suggestedKeeper(g: DuplicateGroup): string | null {
  const own = g.members.filter((m) => !m.is_copy);
  const px = (m: DuplicateMember) => (m.width ?? 0) * (m.height ?? 0);
  const best = [...own].sort(
    (a, b) => px(b) - px(a) || b.viewers - a.viewers || (b.size_bytes ?? 0) - (a.size_bytes ?? 0)
  )[0];
  return best?.video_id ?? null;
}

function resolution(m: DuplicateMember): string {
  return m.width && m.height ? `${m.width}×${m.height}` : "–";
}

function MemberRow({
  g,
  m,
  keeper,
  canChoose,
  onChoose,
}: {
  g: DuplicateGroup;
  m: DuplicateMember;
  keeper: string | null;
  canChoose: boolean;
  onChoose: (videoId: string) => void;
}) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const bytes = useBytes();
  const name = `mkt-q-dup-keeper-${g.id}`;
  const isKeeper = keeper === m.video_id && !m.is_copy;
  return (
    <tr data-keeper={isKeeper || undefined}>
      <td>
        {m.is_copy ? (
          <span className="mkt-q-muted">{t("queue.dup.copy")}</span>
        ) : (
          <label className="mkt-q-check">
            <input
              type="radio"
              name={name}
              checked={isKeeper}
              disabled={!canChoose}
              onChange={() => onChoose(m.video_id)}
            />
            <span className="mkt-visually-hidden">
              {t("queue.dup.keepThis", { name: m.title ?? m.path })}
            </span>
            {isKeeper && <span aria-hidden="true">{t("queue.dup.keeper")}</span>}
          </label>
        )}
      </td>
      <td>
        <Link to={`/queue/items/video/${encodeURIComponent(m.video_id)}`}>
          {bidiIsolate(m.title ?? m.video_id)}
        </Link>
        <div className="mkt-q-fa__paths" dir="ltr">
          {m.path}
        </div>
        {g.library_id === null && <div className="mkt-q-muted">{bidiIsolate(m.library_name)}</div>}
      </td>
      <td className="mkt-q-num">{bytes(m.size_bytes)}</td>
      <td>
        {resolution(m)}
        <div className="mkt-q-muted">
          {[
            m.video_codec,
            m.container,
            m.bitrate_kbps ? t("queue.dup.kbps", { n: n(m.bitrate_kbps) }) : null,
          ]
            .filter(Boolean)
            .join(" · ") || "–"}
        </div>
      </td>
      <td>{fmt.dur(m.duration_sec)}</td>
      <td>
        {m.viewers > 0 ? (
          <>
            {t("queue.dup.watched", { viewers: n(m.viewers), done: n(m.completed) })}
            <div className="mkt-q-muted">{fmt.ago(m.last_watched)}</div>
          </>
        ) : (
          <span className="mkt-q-muted">{t("queue.dup.unwatched")}</span>
        )}
      </td>
    </tr>
  );
}

export function Duplicates() {
  const { t, n } = useI18n();
  const admin = useIsAdmin();
  const [params, setParams] = useSearchParams();
  const status = params.get("status") || "open";
  const tier = params.get("tier") || "";
  const library = params.get("library") || "";
  const [groups, setGroups] = useState<DuplicateGroup[] | null>(null);
  const [next, setNext] = useState<string | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [keepers, setKeepers] = useState<Record<string, string>>({});
  const [notice, setNotice] = useState<string | null>(null);
  const { busy, error, run } = useAction();

  const filter = useCallback(
    () => ({ status, tier: tier || undefined, library_id: library || undefined }),
    [status, tier, library]
  );

  const load = useCallback(() => {
    setLoadError(false);
    queueApi
      .duplicates(filter())
      .then((r) => {
        setGroups(Array.isArray(r?.items) ? r.items : []);
        setNext(r?.next ?? null);
      })
      .catch(() => setLoadError(true));
  }, [filter]);

  useEffect(() => {
    if (admin) load();
  }, [load, admin]);

  const more = () => {
    if (!next) return;
    queueApi
      .duplicates(filter(), next)
      .then((r) => {
        setGroups((prev) => [...(prev ?? []), ...(r?.items ?? [])]);
        setNext(r?.next ?? null);
      })
      .catch(() => setLoadError(true));
  };

  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    setParams(p, { replace: true });
  };

  // the one shown as kept is the one Apply sends: the person's choice, else the
  // group's keeper, else the best picture, then the most watched, then the largest
  const keeperOf = (g: DuplicateGroup) => keepers[g.id] ?? g.keeper_video_id ?? suggestedKeeper(g);

  const apply = async (g: DuplicateGroup) => {
    const chosen = keeperOf(g) ?? undefined;
    const r = await run(`apply:${g.id}`, () => queueApi.applyDuplicate(g.id, chosen));
    if (r) {
      setNotice(t("queue.dup.applied"));
      load();
    }
  };

  const dismiss = async (g: DuplicateGroup) => {
    const r = await run(`dismiss:${g.id}`, () => queueApi.dismissDuplicate(g.id));
    if (r) {
      setNotice(t("queue.dup.dismissed"));
      load();
    }
  };

  const openExact = (groups ?? []).filter(
    (g) =>
      g.tier === "exact" &&
      (g.status === "open" || g.status === "failed") &&
      !(g.job && LIVE.has(g.job.state))
  );
  const applyAllExact = async () => {
    const r = await run("apply-exact", async () => {
      for (const g of openExact) await queueApi.applyDuplicate(g.id, keeperOf(g) ?? undefined);
      return openExact.length;
    });
    if (r !== undefined) {
      setNotice(t("queue.dup.appliedAll", { n: n(r) }));
      load();
    }
  };

  if (!admin) {
    return (
      <section className="mkt-page mkt-q">
        <TitleRow>
          <h1>{t("queue.dup.title")}</h1>
        </TitleRow>
        <p className="mkt-q-empty">{t("queue.failures.adminOnly")}</p>
      </section>
    );
  }

  return (
    <section className="mkt-page mkt-q">
      <header className="mkt-q-header">
        <div className="mkt-q-header__title">
          <TitleRow>
            <h1>{t("queue.dup.title")}</h1>
          </TitleRow>
        </div>
        <div className="mkt-q-header__actions">
          <Link className="mkt-q-linkbtn" to="/queue/file-actions">
            {t("queue.fa.title")}
          </Link>
          <Link className="mkt-q-linkbtn" to="/queue">
            {t("queue.detail.back")}
          </Link>
        </div>
      </header>
      <p className="mkt-q-muted">{t("queue.dup.intro")}</p>

      <form
        className="mkt-q-failures__filters"
        aria-label={t("queue.failures.filters")}
        onSubmit={(e) => e.preventDefault()}
      >
        <label>
          {t("queue.dup.filter.status")}
          <select value={status} onChange={(e) => setParam("status", e.target.value)}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {t(`queue.dup.status.${s}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("queue.dup.filter.tier")}
          <select value={tier} onChange={(e) => setParam("tier", e.target.value)}>
            <option value="">{t("queue.failures.any")}</option>
            <option value="exact">{t("queue.dup.tier.exact")}</option>
            <option value="same_video">{t("queue.dup.tier.same_video")}</option>
          </select>
        </label>
        <label>
          {t("queue.dup.filter.where")}
          <select value={library} onChange={(e) => setParam("library", e.target.value)}>
            <option value="">{t("queue.dup.where.any")}</option>
            {library && library !== "cross" && (
              <option value={library}>
                {groups?.find((g) => g.library_id === library)?.library_name ??
                  t("queue.dup.where.this")}
              </option>
            )}
            <option value="cross">{t("queue.dup.where.cross")}</option>
          </select>
        </label>
        {openExact.length > 0 && (
          <Button
            size="sm"
            variant="secondary"
            type="button"
            loading={busy === "apply-exact"}
            disabled={busy != null && busy !== "apply-exact"}
            onClick={() => void applyAllExact()}
          >
            {t("queue.dup.applyAllExact", { n: n(openExact.length) })}
          </Button>
        )}
      </form>

      {notice && (
        <p role="status" className="mkt-q-note">
          {notice}
        </p>
      )}
      {(error || loadError) && (
        <p role="alert" className="mkt-q-error">
          {error ?? t("queue.error.load")}
        </p>
      )}
      {groups === null && !loadError && <p className="mkt-loading">{t("common.loading")}</p>}
      {groups && groups.length === 0 && <p className="mkt-q-empty">{t("queue.dup.none")}</p>}
      {groups?.map((g) => {
        const decidable = g.status === "open" || g.status === "failed";
        const queued = g.job !== null && LIVE.has(g.job.state);
        const keeper = keeperOf(g);
        const headId = `mkt-q-dup-${g.id}`;
        return (
          <article key={g.id} className="mkt-q-panel mkt-q-dup" aria-labelledby={headId}>
            <div className="mkt-q-section-head">
              <h2 className="mkt-q-section-title" id={headId}>
                {bidiIsolate(
                  g.members.find((m) => m.video_id === keeper)?.title ?? g.members[0]?.title ?? g.id
                )}
              </h2>
              <span className="mkt-q-chip">{t(`queue.dup.tier.${g.tier}`)}</span>
              <span className="mkt-q-chip">{t(`queue.dup.status.${g.status}`)}</span>
              <span className="mkt-q-muted">
                {g.library_name ? bidiIsolate(g.library_name) : t("queue.dup.crossLibrary")}
                {" · "}
                {t("queue.dup.files", { n: n(g.members.length) })}
              </span>
            </div>
            {g.reason && <p className="mkt-q-muted">{g.reason}</p>}
            {queued && (
              <p role="status" className="mkt-q-note">
                {g.job!.outcome === "awaiting_review"
                  ? t("queue.dup.awaiting")
                  : t("queue.dup.queued", { state: t(`queue.items.state.${g.job!.state}`) })}
              </p>
            )}
            {g.job &&
              g.job.state === "done" &&
              g.job.outcome === "awaiting_review" &&
              decidable && <p className="mkt-q-note">{t("queue.dup.awaiting")}</p>}
            <div className="mkt-q-tablewrap">
              <table className="mkt-q-table">
                <thead>
                  <tr>
                    <th scope="col">{t("queue.dup.col.keep")}</th>
                    <th scope="col">{t("queue.dup.col.file")}</th>
                    <th scope="col" className="mkt-q-num">
                      {t("queue.dup.col.size")}
                    </th>
                    <th scope="col">{t("queue.dup.col.video")}</th>
                    <th scope="col">{t("queue.dup.col.length")}</th>
                    <th scope="col">{t("queue.dup.col.watched")}</th>
                  </tr>
                </thead>
                <tbody>
                  {g.members.map((m) => (
                    <MemberRow
                      key={`${m.video_id}|${m.path}`}
                      g={g}
                      m={m}
                      keeper={keeper}
                      canChoose={decidable && !queued}
                      onChoose={(v) => setKeepers((k) => ({ ...k, [g.id]: v }))}
                    />
                  ))}
                </tbody>
              </table>
            </div>
            {decidable && (
              <div className="mkt-q-failures__actions">
                <Button
                  size="sm"
                  loading={busy === `apply:${g.id}`}
                  disabled={queued || (busy != null && busy !== `apply:${g.id}`)}
                  onClick={() => void apply(g)}
                >
                  {t("queue.dup.apply")}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  loading={busy === `dismiss:${g.id}`}
                  disabled={busy != null && busy !== `dismiss:${g.id}`}
                  onClick={() => void dismiss(g)}
                >
                  {t("queue.dup.dismiss")}
                </Button>
              </div>
            )}
          </article>
        );
      })}
      {next && (
        <Button variant="secondary" size="sm" onClick={more}>
          {t("queue.failures.more")}
        </Button>
      )}
    </section>
  );
}
