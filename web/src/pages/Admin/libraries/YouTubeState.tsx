// A library's YouTube on its Manage libraries card (slot 0175), from the one manage read
// (ManagedLibrary.youtube, no request per card):
//
//   one source       "YouTube: 1 source · off" with its switch, and Sources…
//   several          "YouTube: 3 sources · 1 on" and Sources…
//   none             "Add YouTube source" (the same dialog); the channel its files look
//                    like when the API looked (POST …/youtube/guess, an admin's click),
//                    with "Add as source" (off); and a library of a like name that has a
//                    source (Merge into it…, the page's merge dialog)
//
// Every change is the API's to allow (admin only); nothing here turns a source on but
// the switch.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import type { ManagedLibrary } from "../../../lib/libraryAdminApi";
import { queueApi } from "../../../lib/queueApi";
import { bidiIsolate, useI18n } from "../../../lib/i18n";
import { SourceSwitch, switchSource } from "../../../components/queue/YouTubeSourcesDialog";
import { useAction } from "../../../components/queue/useAction";

/** A library name as two names are compared: no case, marks, tatweel or punctuation. */
export function likeNameKey(name: string): string {
  return name
    .normalize("NFKD")
    .replace(/[̀-ًͯ-ٰٟـ]/g, "")
    .replace(/[أإآٱ]/g, "ا")
    .replace(/ى/g, "ي")
    .replace(/ة/g, "ه")
    .toLocaleLowerCase("en")
    .replace(/[^\p{L}\p{N}]+/gu, " ")
    .trim();
}

/**
 * A library with a source whose name holds this one's, or is held in it: maybe the
 * same library twice. Short names (under 4 letters) match too much to say.
 */
export function likeNamed(lib: ManagedLibrary, libraries: ManagedLibrary[]): ManagedLibrary | null {
  const mine = likeNameKey(lib.name);
  if (mine.length < 4) return null;
  for (const other of libraries) {
    if (other.id === lib.id || (other.youtube?.sources ?? 0) === 0) continue;
    const theirs = likeNameKey(other.name);
    if (theirs.length >= 4 && (theirs.includes(mine) || mine.includes(theirs))) return other;
  }
  return null;
}

export function YouTubeState({
  library: lib,
  libraries,
  onSources,
  onMerge,
  onChanged,
}: {
  library: ManagedLibrary;
  libraries: ManagedLibrary[];
  onSources: () => void;
  onMerge: (target: string) => void;
  onChanged: () => void;
}) {
  const { t, n } = useI18n();
  const { busy, error, run } = useAction();
  const [notice, setNotice] = useState<string | null>(null);
  const yt = lib.youtube ?? { sources: 0, on: 0, source: null, guess: null };
  const one = yt.sources === 1 ? yt.source : null;
  const guess = yt.sources === 0 ? yt.guess : null;
  const twin = yt.sources === 0 ? likeNamed(lib, libraries) : null;
  const follower = guess?.followed_by.find((f) => f.id !== lib.id) ?? null;
  const mergeTarget = follower ?? (twin ? { id: twin.id, name: twin.name } : null);

  const done = (msg: string) => {
    setNotice(msg);
    onChanged();
  };
  const toggle = async (on: boolean) => {
    if (!one) return;
    const msg = await run("switch", () => switchSource(lib.id, one.id, on, t));
    if (msg) done(msg);
  };
  const look = async () => {
    const r = await run("guess", () => queueApi.guessSource(lib.id));
    if (r) done(r.external_id ? t("libraryAdmin.yt.found") : t("libraryAdmin.yt.notFound"));
  };
  const addGuess = async () => {
    if (!guess?.url) return;
    const url = guess.url;
    const r = await run("add", () => queueApi.addSource(lib.id, { url, mode: "off" }));
    if (r) done(t("libraryAdmin.yt.addedOff"));
  };

  const summary =
    yt.sources === 0
      ? t("libraryAdmin.yt.none")
      : one
        ? t("libraryAdmin.yt.one", {
            state: t(one.mode === "off" ? "libraryAdmin.yt.state.off" : "libraryAdmin.yt.state.on"),
          })
        : t("libraryAdmin.yt.many", { count: n(yt.sources), on: n(yt.on) });

  return (
    <div
      className="mkt-libadmin__youtube"
      role="group"
      aria-label={t("libraryAdmin.yt.group", { name: lib.name })}
    >
      <div className="mkt-libadmin__youtube-row">
        <span className="mkt-libadmin__youtube-summary">{summary}</span>
        {one && (
          <SourceSwitch source={one} busy={busy === "switch"} onToggle={(on) => void toggle(on)} />
        )}
        {yt.sources > 0 ? (
          <Button type="button" size="sm" variant="secondary" onClick={onSources}>
            {t("queue.offline.sources")}
          </Button>
        ) : (
          <Button type="button" size="sm" variant="secondary" onClick={onSources}>
            {t("libraryAdmin.yt.add")}
          </Button>
        )}
      </div>

      {yt.sources === 0 && guess?.url && (
        <div className="mkt-libadmin__youtube-row">
          <span>
            {t(`libraryAdmin.yt.looksLike.${guess.kind === "playlist" ? "playlist" : "channel"}`, {
              title: bidiIsolate(guess.title || guess.external_id || ""),
            })}{" "}
            <span className="mkt-muted mkt-libadmin__small">
              {t("libraryAdmin.yt.matched", {
                matched: n(guess.ids_matched),
                found: n(guess.ids_found),
                evidence: t(`libraryAdmin.yt.evidence.${guess.evidence ?? "filename"}`),
              })}
            </span>
          </span>
          <Button
            type="button"
            size="sm"
            loading={busy === "add"}
            disabled={busy != null && busy !== "add"}
            onClick={() => void addGuess()}
          >
            {t("libraryAdmin.yt.addAsSource")}
          </Button>
        </div>
      )}
      {yt.sources === 0 && guess && !guess.url && (
        <p className="mkt-muted mkt-libadmin__small">{t("libraryAdmin.yt.notFound")}</p>
      )}
      {yt.sources === 0 && (
        <div className="mkt-libadmin__youtube-row">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            loading={busy === "guess"}
            disabled={busy != null && busy !== "guess"}
            onClick={() => void look()}
          >
            {guess ? t("libraryAdmin.yt.lookAgain") : t("libraryAdmin.yt.lookFor")}
          </Button>
        </div>
      )}

      {mergeTarget && (
        <div className="mkt-libadmin__youtube-row mkt-libadmin__problem">
          <span>
            {follower
              ? t("libraryAdmin.yt.followedBy", { name: bidiIsolate(follower.name) })
              : t("libraryAdmin.yt.sameAs", { name: bidiIsolate(mergeTarget.name) })}
          </span>
          <Button type="button" size="sm" variant="ghost" onClick={() => onMerge(mergeTarget.id)}>
            {t("libraryAdmin.conflicts.mergeInto", { name: bidiIsolate(mergeTarget.name) })}…
          </Button>
        </div>
      )}

      {notice && (
        <p role="status" className="mkt-muted mkt-libadmin__small">
          {notice}
        </p>
      )}
      {error && (
        <p role="alert" className="mkt-libadmin__problem">
          {error}
        </p>
      )}
    </div>
  );
}
