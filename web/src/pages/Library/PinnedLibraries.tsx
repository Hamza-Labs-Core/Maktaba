// Pinned libraries (`/library/pinned`): the user's pins in their order, the
// one place that order is set. A row moves by dragging its handle, from the
// keyboard (focus the handle and use the up / down arrows), or with its
// "move to the top", up and down buttons; its pin icon unpins it. Every change
// saves through lib/pins (PUT /api/me/pinned-libraries), which the sidebar's
// Pinned group and Home follow; a refused save puts the old order back and a
// toast says why. With nothing pinned, the page says how to pin from a card.
import { useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { Link } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { fetchLibrarySummary, type LibrarySummary } from "../../lib/browse";
import { movePinTo, usePins } from "../../lib/pins";
import { PinIcon, PinToggle } from "../../components/PinToggle";
import { TitleRow } from "../../help/HelpLink";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { LinesPlaceholder, Loading } from "../../components/Placeholders";
import { PosterMosaic } from "./Libraries";

export function PinnedLibraries() {
  const { t, n } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const pins = usePins(user?.id);
  // the cards' posters and counts, when the summary answers (the list works without)
  const { data: summary } = usePageData<LibrarySummary[]>("libraries", fetchLibrarySummary, [t]);
  const byId = new Map((summary ?? []).map((l) => [l.id, l]));
  const [dragging, setDragging] = useState<number | null>(null);
  const [over, setOver] = useState<number | null>(null);
  const [announce, setAnnounce] = useState("");
  const rows = useRef(new Map<string, HTMLLIElement | null>());

  async function move(from: number, to: number) {
    if (!pins || to < 0 || to >= pins.length || from === to) return;
    const item = pins[from]!;
    setAnnounce(t("pinned.moved", { name: item.name, pos: n(to + 1), total: n(pins.length) }));
    try {
      await movePinTo(pins, from, to);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("libraries.pin.error")}: ${errorText(e, t)}`,
      });
    }
  }

  // `act` names the control that moved the row: the focus stays on it in the
  // moved row, or goes to the row's handle when it is now disabled (at the top
  // or the bottom)
  function moveAndFocus(from: number, to: number, act: string) {
    const id = pins?.[from]?.id;
    void move(from, to);
    if (!id) return;
    requestAnimationFrame(() => {
      const row = rows.current.get(id);
      const btn = row?.querySelector<HTMLButtonElement>(`[data-act="${act}"]`);
      const target =
        btn && !btn.disabled ? btn : row?.querySelector<HTMLButtonElement>('[data-act="handle"]');
      target?.focus();
    });
  }

  function key(e: KeyboardEvent<HTMLButtonElement>, i: number) {
    const to =
      e.key === "ArrowUp"
        ? i - 1
        : e.key === "ArrowDown"
          ? i + 1
          : e.key === "Home"
            ? 0
            : e.key === "End"
              ? (pins?.length ?? 1) - 1
              : null;
    if (to === null) return;
    e.preventDefault();
    moveAndFocus(i, to, "handle");
  }

  function drop(e: DragEvent, to: number) {
    e.preventDefault();
    if (dragging !== null && dragging !== to) void move(dragging, to);
    setDragging(null);
    setOver(null);
  }

  return (
    <section className="mkt-page mkt-pinned">
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("pinned.title")}</h1>
        </TitleRow>
        <Link className="mkt-btn mkt-btn--ghost" to="/library">
          {t("libraries.all")}
        </Link>
      </header>

      <p className="mkt-pinned__sr" aria-live="polite">
        {announce}
      </p>

      <Loading loading={pins === null}>
        <LinesPlaceholder lines={4} />
      </Loading>

      {pins && pins.length === 0 && (
        <EmptyState
          kind="first_run"
          icon={<PinIcon filled={false} />}
          title={t("pinned.empty.title")}
          description={t("pinned.empty.desc")}
          action={
            <Link className="mkt-btn mkt-btn--primary" to="/library">
              {t("pinned.empty.action")}
            </Link>
          }
        />
      )}

      {pins && pins.length > 0 && (
        <>
          <p className="mkt-muted mkt-pinned__hint">
            {t("libraries.pinned.hint")} {t("pinned.howTo")}
          </p>
          <ol className="mkt-pinned__list" aria-label={t("pinned.title")}>
            {pins.map((p, i) => {
              const lib = byId.get(p.id);
              return (
                <li
                  key={p.id}
                  ref={(el) => {
                    rows.current.set(p.id, el);
                  }}
                  className={[
                    "mkt-pinned__item",
                    dragging === i ? "mkt-pinned__item--drag" : "",
                    over === i && dragging !== i ? "mkt-pinned__item--over" : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  onDragOver={(e) => {
                    if (dragging === null) return;
                    e.preventDefault();
                    e.dataTransfer.dropEffect = "move";
                    setOver(i);
                  }}
                  onDrop={(e) => drop(e, i)}
                >
                  <button
                    type="button"
                    data-act="handle"
                    className="mkt-pinned__handle"
                    draggable
                    aria-label={t("pinned.handle", { name: p.name })}
                    aria-describedby="mkt-pinned-handle-hint"
                    title={t("pinned.handleHint")}
                    onDragStart={(e) => {
                      setDragging(i);
                      e.dataTransfer.effectAllowed = "move";
                      e.dataTransfer.setData("text/plain", p.id);
                    }}
                    onDragEnd={() => {
                      setDragging(null);
                      setOver(null);
                    }}
                    onKeyDown={(e) => key(e, i)}
                  >
                    <span aria-hidden="true">⠿</span>
                  </button>
                  <span className="mkt-pinned__pos" aria-hidden="true">
                    {n(i + 1)}
                  </span>
                  <span className="mkt-pinned__thumb">
                    <PosterMosaic ids={lib?.poster_video_ids ?? []} />
                  </span>
                  <span className="mkt-pinned__text">
                    <Link to={`/library/${encodeURIComponent(p.id)}`} className="mkt-pinned__name">
                      <bdi>{p.name}</bdi>
                    </Link>
                    {lib && (
                      <span className="mkt-pinned__meta">
                        {t("libraries.videos", { n: n(lib.videos ?? 0) })}
                      </span>
                    )}
                  </span>
                  <span className="mkt-pinned__actions">
                    <button
                      type="button"
                      className="mkt-btn mkt-btn--ghost mkt-pinned__btn"
                      aria-label={t("pinned.moveTop", { name: p.name })}
                      title={t("pinned.moveTop", { name: p.name })}
                      disabled={i === 0}
                      data-act="top"
                      onClick={() => moveAndFocus(i, 0, "top")}
                    >
                      <span aria-hidden="true">⤒</span>
                    </button>
                    <button
                      type="button"
                      className="mkt-btn mkt-btn--ghost mkt-pinned__btn"
                      aria-label={t("libraries.moveUp", { name: p.name })}
                      title={t("libraries.moveUp", { name: p.name })}
                      disabled={i === 0}
                      data-act="up"
                      onClick={() => moveAndFocus(i, i - 1, "up")}
                    >
                      <span aria-hidden="true">↑</span>
                    </button>
                    <button
                      type="button"
                      className="mkt-btn mkt-btn--ghost mkt-pinned__btn"
                      aria-label={t("libraries.moveDown", { name: p.name })}
                      title={t("libraries.moveDown", { name: p.name })}
                      disabled={i === pins.length - 1}
                      data-act="down"
                      onClick={() => moveAndFocus(i, i + 1, "down")}
                    >
                      <span aria-hidden="true">↓</span>
                    </button>
                    <PinToggle lib={p} />
                  </span>
                </li>
              );
            })}
          </ol>
          <p id="mkt-pinned-handle-hint" className="mkt-pinned__sr">
            {t("pinned.handleHint")}
          </p>
        </>
      )}
    </section>
  );
}
