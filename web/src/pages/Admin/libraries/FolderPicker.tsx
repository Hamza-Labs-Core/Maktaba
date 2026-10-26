// The folder picker of the Add / Edit library dialogs: browses the server's disk
// through GET /api/libraries/folders. With no folder open it lists the places
// (the path map's targets, [media] roots, the libraries' parent folders, …);
// a folder shows breadcrumbs, Up, and its subfolders. Paths are the database's
// form (what the library stores), always shown left to right.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import {
  libraryAdminApi,
  problemMessage,
  type FolderEntry,
  type Place,
  type FolderListing,
} from "../../../lib/libraryAdminApi";
import { ApiError } from "../../../lib/api";
import { useI18n } from "../../../lib/i18n";

/** The breadcrumbs of a path: "/a/b" → [/, /a, /a/b]; another form is one crumb. */
export function crumbs(path: string): FolderEntry[] {
  if (!path.startsWith("/")) return [{ name: path, path }];
  const parts = path.split("/").filter(Boolean);
  const out: FolderEntry[] = [{ name: "/", path: "/" }];
  let acc = "";
  for (const p of parts) {
    acc += `/${p}`;
    out.push({ name: p, path: acc });
  }
  return out;
}

export function FolderPicker({
  start,
  onPick,
  onCancel,
}: {
  /** the folder to open first (empty: the places) */
  start?: string;
  onPick: (path: string) => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const [places, setPlaces] = useState<Place[] | null>(null);
  const [listing, setListing] = useState<FolderListing | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  /** Opens a folder (null: the places); false when it could not be opened. */
  async function open(path: string | null, keepError = false): Promise<boolean> {
    setBusy(true);
    if (!keepError) setError(null);
    try {
      if (path === null) {
        setPlaces(await libraryAdminApi.places());
        setListing(null);
      } else {
        setListing(await libraryAdminApi.folder(path));
      }
      return true;
    } catch (e) {
      setError(
        e instanceof ApiError && e.status === 404
          ? t("libraryAdmin.picker.notFound")
          : problemMessage(e, t("common.error"))
      );
      return false;
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    // the folder the row holds, else (or when it cannot be opened) the places
    const first = start?.trim() ? start.trim() : null;
    void open(first).then((ok) => {
      if (!ok && first !== null) void open(null, true);
    });
  }, []);

  const entries = listing ? listing.entries : (places ?? []);
  return (
    <div
      className="mkt-libadmin__picker"
      role="group"
      aria-label={t("libraryAdmin.picker.title")}
      aria-busy={busy}
    >
      <div className="mkt-libadmin__picker-bar">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          onClick={() => void open(null)}
          disabled={busy}
        >
          {t("libraryAdmin.picker.places")}
        </Button>
        {listing && (
          <Button
            type="button"
            size="sm"
            variant="ghost"
            onClick={() => void open(listing.parent ? listing.parent : null)}
            disabled={busy}
          >
            {t("libraryAdmin.picker.up")}
          </Button>
        )}
      </div>
      {listing && (
        <nav aria-label={t("libraryAdmin.picker.breadcrumbs")}>
          <ol className="mkt-libadmin__crumbs" dir="ltr">
            {crumbs(listing.path).map((c, i, all) => (
              <li key={c.path}>
                {i === all.length - 1 ? (
                  <span aria-current="location">{c.name}</span>
                ) : (
                  <button
                    type="button"
                    className="mkt-libadmin__crumb"
                    onClick={() => void open(c.path)}
                  >
                    {c.name}
                  </button>
                )}
              </li>
            ))}
          </ol>
        </nav>
      )}
      {listing?.local_path && listing.local_path !== listing.path && (
        <p className="mkt-muted mkt-libadmin__small">
          {t("libraryAdmin.folder.localPath")}{" "}
          <bdi dir="ltr" className="mkt-libadmin__path">
            {listing.local_path}
          </bdi>
        </p>
      )}
      {error && (
        <p className="mkt-error" role="alert">
          {error}
        </p>
      )}
      {!listing && places === null && busy ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : entries.length === 0 ? (
        <p className="mkt-muted">
          {listing ? t("libraryAdmin.picker.noSubfolders") : t("libraryAdmin.picker.noPlaces")}
        </p>
      ) : (
        <ul className="mkt-libadmin__entries" aria-label={t("libraryAdmin.picker.folders")}>
          {entries.map((e) => (
            <li key={e.path}>
              <button
                type="button"
                className="mkt-libadmin__entry"
                onClick={() => void open(e.path)}
                disabled={busy}
                title={e.path}
              >
                <span aria-hidden="true">▸ </span>
                {listing ? (
                  <bdi dir="ltr">{e.name}</bdi>
                ) : (
                  <>
                    <bdi>{e.name}</bdi>{" "}
                    <bdi dir="ltr" className="mkt-muted mkt-libadmin__path">
                      {e.path}
                    </bdi>
                  </>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
      {listing?.truncated && <p className="mkt-muted">{t("libraryAdmin.picker.truncated")}</p>}
      <div className="mkt-libadmin__picker-actions">
        <Button type="button" size="sm" variant="ghost" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        <Button
          type="button"
          size="sm"
          onClick={() => listing && onPick(listing.path)}
          disabled={!listing}
        >
          {t("libraryAdmin.picker.use")}
        </Button>
      </div>
    </div>
  );
}
