// Epic 47: a library's YouTube sources (channels and playlists it follows): a
// dialog on the library dashboard and on Manage libraries, inline in the
// library page's YouTube section. List, add by URL (the API resolves it with
// yt-dlp), switch a source on or off (on restores its last mode), set the mode while it is
// on and the download options, remove it, retry what was gone
// or skipped, resume the back catalogue and import a Tartube database. Every
// change only queues work; the API enforces admin.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { Modal } from "@ds/components/Modal/Modal";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi, SOURCE_MODES } from "../../lib/queueApi";
import type { SourceMode, YouTubeSource } from "../../lib/queueApi";
import { useReadOrder } from "../../lib/readOrder";
import { useFmt } from "./format";
import { useAction } from "./useAction";
// its own styles: Manage libraries opens it too, without the Queues page
import "../../styles/queue.css";

export interface YouTubeSourcesDialogProps {
  library: { id: string; name: string } | null;
  onClose: () => void;
  onChanged?: () => void;
}

const HEIGHTS = [360, 480, 720, 1080, 1440, 2160];

/** The modes a source that is on can have (the switch is its off). */
export const ON_MODES: SourceMode[] = SOURCE_MODES.filter((m) => m !== "off");

/**
 * A source's on/off switch (PATCH {on}): turning it on restores the last mode it had
 * on, else new uploads and the back catalogue; the API decides and enforces admin.
 */
export function SourceSwitch({
  source,
  busy,
  onToggle,
}: {
  source: Pick<YouTubeSource, "id" | "external_id" | "mode">;
  busy: boolean;
  onToggle: (on: boolean) => void;
}) {
  const { t } = useI18n();
  const on = source.mode !== "off";
  return (
    <Toggle
      className="mkt-q-ytsrc__switch"
      checked={on}
      aria-checked={on}
      disabled={busy}
      onChange={(e) => onToggle(e.target.checked)}
      label={
        <>
          {t("queue.yt.switch")}
          <span className="mkt-visually-hidden"> {source.external_id}</span>
        </>
      }
    />
  );
}

/** PATCH {on} and the notice to show for it. */
export async function switchSource(
  libraryId: string,
  sourceId: string,
  on: boolean,
  t: (key: string) => string
): Promise<string> {
  const r = await queueApi.patchSource(libraryId, sourceId, { on });
  if (!on) return t("queue.yt.modeSet.off");
  return r.refresh ? t("queue.yt.onRefresh") : t(`queue.yt.modeSet.${r.source.mode}`);
}

function OptionsEditor({
  source,
  busy,
  onSave,
  onCancel,
}: {
  source: YouTubeSource;
  busy: boolean;
  onSave: (o: Record<string, unknown>) => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const o = source.options ?? {};
  const [height, setHeight] = useState(String(o.max_height ?? 1080));
  const [langs, setLangs] = useState(o.sub_langs ?? "ar,en");
  const [autoSubs, setAutoSubs] = useState(o.auto_subs ?? true);
  const [audioOnly, setAudioOnly] = useState(o.audio_only ?? false);
  const [cookies, setCookies] = useState(o.cookies_from_browser ?? "");
  const [rate, setRate] = useState(o.limit_rate ?? "");
  const id = `mkt-q-yt-opts-${source.id}`;
  return (
    <form
      className="mkt-q-ytsrc__opts"
      aria-label={t("queue.yt.options")}
      onSubmit={(e) => {
        e.preventDefault();
        onSave({
          max_height: Number(height),
          sub_langs: langs.trim(),
          auto_subs: autoSubs,
          audio_only: audioOnly,
          cookies_from_browser: cookies.trim() || null,
          limit_rate: rate.trim() || null,
        });
      }}
    >
      <label htmlFor={`${id}-h`}>
        {t("queue.yt.opt.maxHeight")}
        <select id={`${id}-h`} value={height} onChange={(e) => setHeight(e.target.value)}>
          {HEIGHTS.map((h) => (
            <option key={h} value={h}>
              {h}p
            </option>
          ))}
        </select>
      </label>
      <label htmlFor={`${id}-l`}>
        {t("queue.yt.opt.subLangs")}
        <input
          id={`${id}-l`}
          dir="ltr"
          value={langs}
          onChange={(e) => setLangs(e.target.value)}
          placeholder="ar,en"
        />
      </label>
      <label className="mkt-q-check">
        <input type="checkbox" checked={autoSubs} onChange={(e) => setAutoSubs(e.target.checked)} />
        {t("queue.yt.opt.autoSubs")}
      </label>
      <label className="mkt-q-check">
        <input
          type="checkbox"
          checked={audioOnly}
          onChange={(e) => setAudioOnly(e.target.checked)}
        />
        {t("queue.yt.opt.audioOnly")}
      </label>
      <label htmlFor={`${id}-c`}>
        {t("queue.yt.opt.cookies")}
        <input
          id={`${id}-c`}
          dir="ltr"
          value={cookies}
          onChange={(e) => setCookies(e.target.value)}
          placeholder="firefox"
        />
      </label>
      <label htmlFor={`${id}-r`}>
        {t("queue.yt.opt.limitRate")}
        <input
          id={`${id}-r`}
          dir="ltr"
          value={rate}
          onChange={(e) => setRate(e.target.value)}
          placeholder="2M"
        />
      </label>
      <div className="mkt-q-failures__actions">
        <Button size="sm" type="submit" loading={busy}>
          {t("queue.yt.save")}
        </Button>
        <Button size="sm" variant="ghost" type="button" onClick={onCancel}>
          {t("queue.retrigger.cancel")}
        </Button>
      </div>
    </form>
  );
}

/** A library's sources: the dialog's content, and the YouTube section of the library page. */
export function YouTubeSourcesPanel({
  library,
  onChanged,
}: {
  library: { id: string; name: string } | null;
  onChanged?: () => void;
}) {
  const { t, n } = useI18n();
  const fmt = useFmt();
  const { busy, error, run: runAction, clear } = useAction();
  // the list's reads and the changes made here, in order: a read that began
  // before a change (its answer still has the removed source, the old mode) is dropped
  const order = useReadOrder();
  const run = <T,>(key: string, fn: () => Promise<T>) => runAction(key, () => order.saving(fn));
  const [items, setItems] = useState<YouTubeSource[] | null>(null);
  const [loadError, setLoadError] = useState(false);
  const [url, setUrl] = useState("");
  const [mode, setMode] = useState<SourceMode>("new_and_backfill");
  const [folder, setFolder] = useState("");
  const [editing, setEditing] = useState<string | null>(null);
  const [tartube, setTartube] = useState<{ open: boolean; path: string; channels: string }>({
    open: false,
    path: "",
    channels: "",
  });
  const [notice, setNotice] = useState<string | null>(null);
  const libId = library?.id ?? null;

  const load = useCallback(() => {
    if (!libId) return;
    setLoadError(false);
    const ticket = order.begin();
    queueApi
      .sources(libId)
      .then((r) => order.fresh(ticket) && setItems(Array.isArray(r?.items) ? r.items : []))
      .catch(() => order.fresh(ticket) && setLoadError(true));
  }, [libId, order]);

  useEffect(() => {
    setItems(null);
    setNotice(null);
    setEditing(null);
    setUrl("");
    setFolder("");
    setMode("new_and_backfill");
    setTartube({ open: false, path: "", channels: "" });
    clear();
    // another library's list still on its way is not this one's
    order.changed();
    load();
  }, [load, clear, order]);

  if (!library) return null;

  const after = (msg: string) => {
    setNotice(msg);
    load();
    onChanged?.();
  };

  const add = async () => {
    const r = await run("add", () =>
      queueApi.addSource(library.id, {
        url: url.trim(),
        mode,
        folder: folder.trim() || undefined,
      })
    );
    if (r) {
      setUrl("");
      setFolder("");
      after(
        r.refresh
          ? t("queue.yt.addedRefresh", { kind: r.source.kind })
          : t("queue.yt.added", { kind: r.source.kind })
      );
    }
  };

  const setSourceMode = async (s: YouTubeSource, m: SourceMode) => {
    const r = await run(`mode:${s.id}`, () => queueApi.patchSource(library.id, s.id, { mode: m }));
    if (r) after(r.refresh ? t("queue.yt.onRefresh") : t(`queue.yt.modeSet.${m}`));
  };

  const toggle = async (s: YouTubeSource, on: boolean) => {
    const msg = await run(`mode:${s.id}`, () => switchSource(library.id, s.id, on, t));
    if (msg) after(msg);
  };

  const saveOptions = async (s: YouTubeSource, o: Record<string, unknown>) => {
    const r = await run(`opts:${s.id}`, () =>
      queueApi.patchSource(library.id, s.id, { options: o })
    );
    if (r) {
      setEditing(null);
      after(t("queue.yt.optionsSaved"));
    }
  };

  const remove = async (s: YouTubeSource) => {
    if (!window.confirm(t("queue.yt.confirmRemove", { url: s.url }))) return;
    const r = await run(`del:${s.id}`, async () => {
      await queueApi.deleteSource(library.id, s.id);
      return true;
    });
    if (r) after(t("queue.yt.removed"));
  };

  const retry = async (sourceId?: string) => {
    const r = await run(`retry:${sourceId ?? "all"}`, () =>
      queueApi.retryDownloads(library.id, {
        states: ["gone", "skipped", "failed"],
        source_id: sourceId,
      })
    );
    if (r) after(t("queue.yt.retried", { n: n(r.wanted) }));
  };

  const resume = async () => {
    const r = await run("backfill", () => queueApi.resumeBackfill(library.id));
    if (r)
      after(
        r.skipped > 0 ? t("queue.offline.notSetUp", { n: n(r.skipped) }) : t("queue.yt.resumed")
      );
  };

  const importTartube = async () => {
    const channels = tartube.channels
      .split(",")
      .map((c) => c.trim())
      .filter(Boolean);
    const r = await run("tartube", () =>
      queueApi.importTartube(library.id, {
        path: tartube.path.trim(),
        channels: channels.length ? channels : undefined,
      })
    );
    if (r) {
      setTartube({ open: false, path: "", channels: "" });
      after(t("queue.yt.tartubeQueued"));
    }
  };

  const failedTotal = (items ?? []).reduce(
    (a, s) => a + s.downloads.failed + s.downloads.gone + s.downloads.skipped,
    0
  );
  const backlog = (items ?? []).reduce((a, s) => a + s.downloads.backfill, 0);

  return (
    <div className="mkt-q-ytsrc">
      <form
        className="mkt-q-ytsrc__add"
        aria-label={t("queue.yt.add")}
        onSubmit={(e) => {
          e.preventDefault();
          if (url.trim()) void add();
        }}
      >
        <label htmlFor="mkt-q-yt-url">{t("queue.yt.url")}</label>
        <input
          id="mkt-q-yt-url"
          type="url"
          dir="ltr"
          required
          value={url}
          placeholder="https://www.youtube.com/@channel"
          onChange={(e) => setUrl(e.target.value)}
        />
        <label htmlFor="mkt-q-yt-mode">{t("queue.yt.mode")}</label>
        <select
          id="mkt-q-yt-mode"
          value={mode}
          onChange={(e) => setMode(e.target.value as SourceMode)}
        >
          {SOURCE_MODES.map((m) => (
            <option key={m} value={m}>
              {t(`queue.yt.modes.${m}`)}
            </option>
          ))}
        </select>
        <label htmlFor="mkt-q-yt-folder">{t("queue.yt.folder")}</label>
        <input
          id="mkt-q-yt-folder"
          dir="auto"
          value={folder}
          placeholder={t("queue.yt.folderAuto")}
          onChange={(e) => setFolder(e.target.value)}
        />
        <Button type="submit" size="sm" loading={busy === "add"} disabled={!url.trim()}>
          {t("queue.yt.addButton")}
        </Button>
      </form>

      {notice && (
        <p role="status" className="mkt-q-note">
          {notice}
        </p>
      )}
      {error && (
        <p role="alert" className="mkt-q-error">
          {error}
        </p>
      )}
      {loadError && (
        <p role="alert" className="mkt-q-error">
          {t("queue.error.load")}
        </p>
      )}
      {items === null && !loadError && <p className="mkt-loading">{t("common.loading")}</p>}
      {items && items.length === 0 && <p className="mkt-q-empty">{t("queue.yt.none")}</p>}
      {items && items.length > 0 && (
        <ul className="mkt-q-ytsrc__list">
          {items.map((s) => (
            <li key={s.id} className="mkt-q-ytsrc__item" data-mode={s.mode}>
              <div className="mkt-q-ytsrc__head">
                <span className="mkt-q-chip">{t(`queue.yt.kind.${s.kind}`)}</span>
                <a href={s.url} target="_blank" rel="noreferrer" dir="ltr">
                  {s.external_id}
                </a>
                {s.folder && <span className="mkt-q-muted">· {bidiIsolate(s.folder)}</span>}
              </div>
              <div className="mkt-q-ytsrc__meta">
                <span>
                  {t("queue.yt.counts", {
                    wanted: n(s.downloads.wanted),
                    backfill: n(s.downloads.backfill),
                    done: n(s.downloads.done),
                    failed: n(s.downloads.failed),
                    skipped: n(s.downloads.skipped),
                    gone: n(s.downloads.gone),
                  })}
                </span>
                <span className="mkt-q-muted">
                  {t("queue.yt.checked", { time: fmt.ago(s.last_checked_at) })}
                </span>
                {s.status && s.status !== "ok" && (
                  <span className="mkt-q-error" dir="auto">
                    {t(`queue.yt.status.${s.status}`)}
                    {s.error ? `: ${s.error}` : ""}
                  </span>
                )}
              </div>
              <div className="mkt-q-ytsrc__actions">
                <SourceSwitch
                  source={s}
                  busy={busy === `mode:${s.id}`}
                  onToggle={(on) => void toggle(s, on)}
                />
                {s.mode !== "off" && (
                  <label>
                    <span className="mkt-visually-hidden">
                      {t("queue.yt.modeOf", { source: s.external_id })}
                    </span>
                    <select
                      value={s.mode}
                      disabled={busy === `mode:${s.id}`}
                      onChange={(e) => void setSourceMode(s, e.target.value as SourceMode)}
                    >
                      {ON_MODES.map((m) => (
                        <option key={m} value={m}>
                          {t(`queue.yt.modes.${m}`)}
                        </option>
                      ))}
                    </select>
                  </label>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  aria-expanded={editing === s.id}
                  onClick={() => setEditing(editing === s.id ? null : s.id)}
                >
                  {t("queue.yt.options")}
                  <span className="mkt-visually-hidden"> {s.external_id}</span>
                </Button>
                {s.downloads.failed + s.downloads.gone + s.downloads.skipped > 0 && (
                  <Button
                    size="sm"
                    variant="ghost"
                    disabled={busy != null}
                    onClick={() => void retry(s.id)}
                  >
                    {t("queue.yt.retrySource", {
                      n: n(s.downloads.failed + s.downloads.gone + s.downloads.skipped),
                    })}
                    <span className="mkt-visually-hidden"> {s.external_id}</span>
                  </Button>
                )}
                <Button
                  size="sm"
                  variant="ghost"
                  disabled={busy != null}
                  onClick={() => void remove(s)}
                >
                  {t("queue.yt.remove")}
                  <span className="mkt-visually-hidden"> {s.external_id}</span>
                </Button>
              </div>
              {editing === s.id && (
                <OptionsEditor
                  source={s}
                  busy={busy === `opts:${s.id}`}
                  onSave={(o) => void saveOptions(s, o)}
                  onCancel={() => setEditing(null)}
                />
              )}
            </li>
          ))}
        </ul>
      )}

      <div className="mkt-q-ytsrc__lib" role="group" aria-label={t("queue.yt.libraryActions")}>
        <Button
          size="sm"
          variant="secondary"
          loading={busy === "backfill"}
          disabled={busy != null && busy !== "backfill"}
          onClick={() => void resume()}
        >
          {t("queue.yt.resumeBackfill", { n: n(backlog) })}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          loading={busy === "retry:all"}
          disabled={failedTotal === 0 || (busy != null && busy !== "retry:all")}
          onClick={() => void retry()}
        >
          {t("queue.yt.retryAll", { n: n(failedTotal) })}
        </Button>
        <Button
          size="sm"
          variant="ghost"
          aria-expanded={tartube.open}
          onClick={() => setTartube((v) => ({ ...v, open: !v.open }))}
        >
          {t("queue.yt.tartube")}
        </Button>
      </div>
      {tartube.open && (
        <form
          className="mkt-q-ytsrc__opts"
          aria-label={t("queue.yt.tartube")}
          onSubmit={(e) => {
            e.preventDefault();
            if (tartube.path.trim()) void importTartube();
          }}
        >
          <p className="mkt-q-muted">{t("queue.yt.tartubeHelp")}</p>
          <label htmlFor="mkt-q-yt-tt-path">
            {t("queue.yt.tartubePath")}
            <input
              id="mkt-q-yt-tt-path"
              dir="ltr"
              required
              value={tartube.path}
              placeholder="/Volumes/Originals/Backup/Youtube/tartube_db_export.json"
              onChange={(e) => setTartube((v) => ({ ...v, path: e.target.value }))}
            />
          </label>
          <label htmlFor="mkt-q-yt-tt-ch">
            {t("queue.yt.tartubeChannels")}
            <input
              id="mkt-q-yt-tt-ch"
              dir="auto"
              value={tartube.channels}
              onChange={(e) => setTartube((v) => ({ ...v, channels: e.target.value }))}
            />
          </label>
          <Button
            size="sm"
            type="submit"
            loading={busy === "tartube"}
            disabled={!tartube.path.trim()}
          >
            {t("queue.yt.tartubeRun")}
          </Button>
        </form>
      )}
    </div>
  );
}

export function YouTubeSourcesDialog({ library, onClose, onChanged }: YouTubeSourcesDialogProps) {
  const { t } = useI18n();
  if (!library) return null;
  return (
    <Modal
      open
      onClose={onClose}
      title={t("queue.yt.title", { name: bidiIsolate(library.name) })}
      footer={<Button onClick={onClose}>{t("queue.retrigger.close")}</Button>}
    >
      <YouTubeSourcesPanel library={library} onChanged={onChanged} />
    </Modal>
  );
}
