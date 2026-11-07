// /review — word review (Epic 52, story 52.11): the suspect words of every transcript, one
// at a time, for a home admin. The word opens with its sentence and its clip playing; one
// key resolves it, the next word opens by itself, and every action has an undo. Keys are
// physical (KeyboardEvent.code) so they work with an Arabic keyboard layout, and are off
// while the edit field has focus. Only home admins see the page (README §8, decision 1);
// the API's 403 is the rule.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useWindowKeydown } from "../../lib/useWindowKeydown";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../lib/i18n";
import { useIsAdmin } from "../../components/queue/useIsAdmin";
import { useOptionalAuth } from "../../lib/auth";
import { pluralKey } from "../../lib/plural";
import { ApiError } from "../../lib/api";
import {
  reviewApi,
  type Action,
  type Clip,
  type Flag,
  type FlagDetail,
  type ResolveResult,
} from "../../lib/reviewApi";
import { ClipPlayer, type ClipControls } from "./ClipPlayer";
import { WordPanel } from "./WordPanel";
import { Shortcuts } from "./Shortcuts";
import { keyAction } from "./keys";
import "../../styles/review.css";

export interface Undoable {
  label: string;
  editId: string | null;
  batchId: string | null;
  flagId: string;
}

export function Review() {
  const { t, locale } = useI18n();
  const isAdmin = useIsAdmin();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const [items, setItems] = useState<Flag[] | null>(null);
  const [index, setIndex] = useState(0);
  const [detail, setDetail] = useState<FlagDetail | null>(null);
  const [clip, setClip] = useState<Clip | null>(null);
  const [error, setError] = useState<"forbidden" | "load" | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [applyAll, setApplyAll] = useState(false);
  const [edit, setEdit] = useState("");
  const [help, setHelp] = useState(false);
  const [busy, setBusy] = useState(false);
  const [lastCheck, setLastCheck] = useState<string | null>(null);
  const [live, setLive] = useState("");
  const undoStack = useRef<Undoable[]>([]);
  const field = useRef<HTMLInputElement | null>(null);
  const player = useRef<ClipControls | null>(null);

  const filters = useMemo(
    () => ({
      library: params.get("library") ?? undefined,
      language: params.get("language") ?? undefined,
      reason: params.get("reason") ?? undefined,
      status: (params.get("status") as Flag["status"] | null) ?? undefined,
      show_locked: params.get("show_locked") === "true",
    }),
    [params]
  );

  const load = useCallback(async () => {
    try {
      const page = await reviewApi.flags({ ...filters, limit: 100 });
      setItems(page.items.filter((f) => f.kind === "word"));
      setIndex(0);
      setError(null);
      if (!page.items.length) {
        const s = await reviewApi.summary().catch(() => null);
        setLastCheck(s?.last_check ?? null);
      }
    } catch (e) {
      setError(
        e instanceof ApiError && (e.status === 403 || e.status === 401) ? "forbidden" : "load"
      );
    }
  }, [filters]);

  useEffect(() => {
    if (isAdmin) void load();
  }, [isAdmin, load]);

  const current = items && items[index];

  // open the word: its context (and its lock), its clip; the next ones are prefetched
  useEffect(() => {
    let gone = false;
    setDetail(null);
    setClip(null);
    setNotice(null);
    if (!current) return;
    void reviewApi
      .flag(current.id)
      .then((d) => {
        if (gone) return;
        setDetail(d);
        setEdit(d.original);
        setApplyAll(false);
        if (d.status === "stale") setNotice(t("review.stale"));
      })
      .catch(() => !gone && setNotice(t("review.loadFailed")));
    void reviewApi
      .clip(current.id)
      .then((c) => !gone && setClip(c))
      .catch(() => !gone && setClip(null));
    for (const next of (items ?? []).slice(index + 1, index + 4))
      void reviewApi.clip(next.id).catch(() => null);
    return () => {
      gone = true;
    };
  }, [current, index, items, t]);

  const myId = useOptionalAuth()?.user?.id ?? null;
  const [takeOver, setTakeOver] = useState(false);
  useEffect(() => setTakeOver(false), [current?.id]);
  // opening the word took its lock when it was free: a holder now is another reviewer
  const heldByOther = !!(
    detail?.locked_by &&
    detail.locked_by !== myId &&
    detail.locked_until &&
    new Date(detail.locked_until) > new Date()
  );

  const advance = useCallback(() => {
    setItems((list) => {
      if (!list) return list;
      const out = list.filter((_, i) => i !== index);
      setIndex((i) => Math.min(i, Math.max(0, out.length - 1)));
      return out;
    });
  }, [index]);

  const resolve = useCallback(
    async (action: Action, text?: string) => {
      if (!detail || busy) return;
      if (heldByOther && !takeOver) return;
      setBusy(true);
      try {
        const res: ResolveResult = await reviewApi.resolve(detail.id, {
          action,
          text,
          apply_to_same_key: applyAll,
          force: takeOver,
        });
        const label =
          action === "correct"
            ? t("review.fixed", { from: detail.original, to: text ?? "" })
            : t(`review.done.${action}`, { word: detail.original });
        undoStack.current.push({
          label,
          editId: res.edit_id,
          batchId: res.batch_id,
          flagId: detail.id,
        });
        setLive(label);
        toast.show({
          id: `review-${detail.id}`,
          tone: "success",
          message: (
            <span className="mkt-review-toast">
              <span>{label}</span>
              <button type="button" onClick={() => void undo()}>
                {t("review.undo")}
              </button>
            </span>
          ),
        });
        advance();
      } catch (e) {
        const code = e instanceof ApiError ? e.problem.code : undefined;
        if (code === "locked")
          setNotice(
            t("review.lockedBy", {
              name:
                ((e as ApiError).problem as unknown as { locked_by_name?: string })
                  .locked_by_name ?? "",
            })
          );
        else if (code === "stale" || code === "resolved") {
          setNotice(t("review.stale"));
          void load();
        } else setNotice(t("review.actionFailed"));
      } finally {
        setBusy(false);
      }
    },
    [detail, busy, applyAll, takeOver, heldByOther, t, toast, advance, load]
  );

  const undo = useCallback(async () => {
    const last = undoStack.current.pop();
    if (!last) return;
    try {
      if (last.batchId) await reviewApi.undoBatch(last.batchId);
      else if (last.editId) await reviewApi.undoEdit(last.editId);
      setLive(t("review.undone"));
      toast.show({ id: "review-undo", tone: "info", message: t("review.undone") });
      await load();
    } catch {
      toast.show({ id: "review-undo", tone: "warn", message: t("review.undoFailed") });
    }
  }, [load, t, toast]);

  // keys act on the word on screen (lib/useWindowKeydown, #697)
  useWindowKeydown((e: KeyboardEvent) => {
    const inField = document.activeElement === field.current;
    const act = keyAction(e, inField);
    if (!act) return;
    e.preventDefault();
    e.stopPropagation();
    switch (act.kind) {
      case "next":
        setIndex((i) => Math.min(i + 1, Math.max(0, (items?.length ?? 1) - 1)));
        break;
      case "prev":
        setIndex((i) => Math.max(0, i - 1));
        break;
      case "play":
        player.current?.toggle(act.slow ? 0.75 : 1);
        break;
      case "replay":
        player.current?.replay();
        break;
      case "accept":
        if (inField && edit.trim() && edit !== detail?.original)
          void resolve("correct", edit.trim());
        else void resolve("accept");
        break;
      case "candidate": {
        const c = detail?.candidates[act.n];
        if (c) void resolve("correct", c.text);
        break;
      }
      case "edit":
        field.current?.focus();
        field.current?.select();
        break;
      case "name":
        void resolve("name");
        break;
      case "dictionary":
        if (!detail?.reasons.includes("quran_mismatch")) void resolve("dictionary");
        break;
      case "skip":
        void resolve("skip");
        break;
      case "applyAll":
        setApplyAll((v) => !v);
        break;
      case "undo":
        void undo();
        break;
      case "help":
        setHelp((v) => !v);
        break;
      case "leave":
        field.current?.blur();
        break;
    }
  }, true);

  if (!isAdmin || error === "forbidden")
    return (
      <main className="mkt-review" aria-labelledby="review-title">
        <h1 id="review-title">{t("review.title")}</h1>
        <p role="alert">{t("review.adminsOnly")}</p>
      </main>
    );

  const setFilter = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    setParams(p, { replace: true });
  };

  return (
    <main className="mkt-review" aria-labelledby="review-title">
      <header className="mkt-review-head">
        <h1 id="review-title">{t("review.title")}</h1>
        <nav className="mkt-review-links">
          <Link to="/review/history">{t("review.history.title")}</Link>
          <Link to="/review/vocabulary">{t("vocabulary.title")}</Link>
          <Link to="/review/languages">{t("wordReview.packs.title")}</Link>
          <Link to="/help/word-review">{t("review.helpLink")}</Link>
          <button type="button" onClick={() => setHelp(true)}>
            {t("review.shortcuts.open")}
          </button>
        </nav>
      </header>
      <div className="mkt-review-filters" role="group" aria-label={t("review.filters.label")}>
        <label>
          {t("review.filters.reason")}
          <select
            value={params.get("reason") ?? ""}
            onChange={(e) => setFilter("reason", e.target.value)}
          >
            <option value="">{t("review.filters.any")}</option>
            {[
              "low_confidence",
              "not_in_dictionary",
              "listeners_disagree",
              "quran_mismatch",
              "named_entity",
              "near_vocabulary",
              "manual",
              "previous_fix",
            ].map((r) => (
              <option key={r} value={r}>
                {t(`review.reason.${r}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("review.filters.language")}
          <input
            value={params.get("language") ?? ""}
            onChange={(e) => setFilter("language", e.target.value)}
            size={4}
          />
        </label>
        <label>
          <input
            type="checkbox"
            checked={params.get("show_locked") === "true"}
            onChange={(e) => setFilter("show_locked", e.target.checked ? "true" : "")}
          />
          {t("review.filters.showLocked")}
        </label>
      </div>
      <div aria-live="polite" className="mkt-visually-hidden">
        {live}
      </div>
      {error === "load" && <p role="alert">{t("review.loadFailed")}</p>}
      {items && items.length === 0 && (
        <p className="mkt-review-empty">
          {t("review.empty")}
          {lastCheck && (
            <> · {t("review.lastCheck", { when: new Date(lastCheck).toLocaleString(locale) })}</>
          )}
        </p>
      )}
      {items && items.length > 0 && (
        <div className="mkt-review-body">
          <ol className="mkt-review-list" aria-label={t("review.list")}>
            {items.map((f, i) => (
              <li key={f.id}>
                <button
                  type="button"
                  aria-current={i === index ? "true" : undefined}
                  onClick={() => setIndex(i)}
                >
                  <bdi dir={f.direction}>{f.original}</bdi>
                  <span className="mkt-review-reasons">
                    {f.reasons.map((r) => (
                      <span key={r} className={`mkt-review-reason mkt-review-reason-${r}`}>
                        {t(`review.reason.${r}`)}
                      </span>
                    ))}
                  </span>
                  <span className="mkt-review-video">{f.video_title}</span>
                  <span className="mkt-review-lang">{f.language}</span>
                </button>
              </li>
            ))}
          </ol>
          <section className="mkt-review-main" aria-label={t("review.word")}>
            {notice && <p role="status">{notice}</p>}
            {detail && (
              <>
                {detail.reviewers_note && (
                  <p className="mkt-review-note">{detail.reviewers_note}</p>
                )}
                {heldByOther && !takeOver && (
                  <p role="status">
                    {t("review.lockedBy", { name: detail.locked_by_name ?? "" })}{" "}
                    <button type="button" onClick={() => setTakeOver(true)}>
                      {t("review.takeOver")}
                    </button>
                  </p>
                )}
                <ClipPlayer clip={clip} controls={player} />
                <WordPanel
                  detail={detail}
                  edit={edit}
                  onEdit={setEdit}
                  field={field}
                  disabled={busy || (heldByOther && !takeOver)}
                  onAction={(a, text) => void resolve(a, text)}
                />
                {detail.same_key_open > 0 && (
                  <div className="mkt-review-same">
                    <label>
                      <input
                        type="checkbox"
                        checked={applyAll}
                        onChange={(e) => setApplyAll(e.target.checked)}
                      />
                      {t(pluralKey(locale, "review.applyAll", detail.same_key_open + 1), {
                        count: detail.same_key_open + 1,
                      })}
                    </label>
                    <p>
                      {t(pluralKey(locale, "review.otherPlaces", detail.same_key_open), {
                        count: detail.same_key_open,
                      })}
                    </p>
                    <ul>
                      {detail.same_key_preview.map((p) => (
                        <li key={p.flag_id}>
                          <bdi dir={detail.direction}>{p.context}</bdi>{" "}
                          <Link
                            to={`/videos/${p.video_id}${p.start_sec != null ? `?t=${Math.floor(p.start_sec)}` : ""}`}
                          >
                            {p.video_title}
                          </Link>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </>
            )}
          </section>
        </div>
      )}
      {help && <Shortcuts onClose={() => setHelp(false)} />}
    </main>
  );
}
