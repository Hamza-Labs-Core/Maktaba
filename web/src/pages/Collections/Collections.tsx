// Epic 45 Phase 3 (story 45.17) — a library's collections
// (`/library/:libraryId/collections`): a card per collection (a poster mosaic,
// the name, how many videos, or "Saved filter" for a smart one). Admins also
// get a form to make a new, empty collection; a smart one is saved from the
// Browse tab's filters (LibraryBrowser, "Save as collection"). A library the
// user may not see answers 404, the same as one that does not exist.
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import {
  COLLECTION_NAME_MAX,
  collectionPath,
  createCollection,
  fetchCollections,
  type Collection,
} from "../../lib/collections";
import { TitleRow } from "../../help/HelpLink";
import { PosterMosaic } from "../Library/Libraries";
import { LoadError } from "../../components/LoadError";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { CardGridPlaceholder, Loading } from "../../components/Placeholders";

/** A collection's card: its mosaic, name and size (a row item or a grid cell). */
export function CollectionCard({ c, className }: { c: Collection; className: string }) {
  const { t, n } = useI18n();
  return (
    <li className={className}>
      <Link to={collectionPath(c)} className="mkt-libcard__link">
        <PosterMosaic ids={c.poster_video_ids} />
        <bdi className="mkt-libcard__name">{c.name}</bdi>
        <span className="mkt-libcard__meta mkt-muted">
          {c.is_smart ? (
            <Badge tone="accent">{t("collections.smart")}</Badge>
          ) : (
            t("libraries.videos", { n: n(c.videos ?? 0) })
          )}
        </span>
      </Link>
    </li>
  );
}

export function Collections() {
  const { libraryId = "" } = useParams();
  return <CollectionsOf key={libraryId} libraryId={libraryId} />;
}

function CollectionsOf({ libraryId }: { libraryId: string }) {
  const { t } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const navigate = useNavigate();
  const {
    data,
    err: loadErr,
    loading,
    reload,
  } = usePageData(
    `collections:${libraryId}`,
    async () => {
      const [items, lib] = await Promise.all([
        fetchCollections(libraryId),
        api.get<{ name?: string }>(`/api/libraries/${encodeURIComponent(libraryId)}`),
      ]);
      return { items, libName: lib?.name ?? "" };
    },
    [t]
  );
  const items = data?.items ?? null;
  const libName = data?.libName ?? "";
  const notFound = loadErr instanceof ApiError && loadErr.status === 404;
  const err = notFound ? null : loadErr;
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);

  async function create() {
    const nm = name.trim();
    if (!nm) return;
    setCreating(true);
    try {
      const c = await createCollection(libraryId, nm);
      setName("");
      navigate(collectionPath(c));
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("collections.createError")}: ${errorText(e, t)}`,
      });
    } finally {
      setCreating(false);
    }
  }

  const libraryLink = (
    <Link className="mkt-btn mkt-btn--primary" to="/library">
      {t("libraryPage.backToLibraries")}
    </Link>
  );

  if (notFound) {
    return (
      <section className="mkt-page mkt-collections">
        <ErrorState
          kind="not_found"
          title={t("libraryPage.notFound")}
          description={t("libraryPage.notFound.desc")}
          action={libraryLink}
        />
      </section>
    );
  }

  return (
    <section className="mkt-page mkt-collections">
      <Link className="mkt-crumb" to={`/library/${encodeURIComponent(libraryId)}`}>
        <bdi>{libName || t("libraryPage.tab.overview")}</bdi>
      </Link>
      <header className="mkt-page__header">
        <TitleRow>
          <h1>{t("collections.title")}</h1>
        </TitleRow>
      </header>
      {user?.is_admin && (
        <div className="mkt-collections__admin">
          <form
            className="mkt-playlists__new"
            onSubmit={(e) => {
              e.preventDefault();
              void create();
            }}
          >
            <label className="mkt-field">
              <span className="mkt-field__label">{t("collections.newName")}</span>
              <input
                type="text"
                dir="auto"
                value={name}
                maxLength={COLLECTION_NAME_MAX}
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <Button type="submit" disabled={creating || !name.trim()} loading={creating}>
              {t("collections.create")}
            </Button>
          </form>
          <p className="mkt-muted">{t("collections.smartHint")}</p>
        </div>
      )}

      {err != null && <LoadError error={err} onRetry={reload} />}

      <Loading loading={loading}>
        <CardGridPlaceholder cards={3} />
      </Loading>

      {items && items.length === 0 && (
        <EmptyState
          kind="first_run"
          title={t("collections.empty.title")}
          description={
            user?.is_admin ? t("collections.empty.descAdmin") : t("collections.empty.desc")
          }
        />
      )}

      {items && items.length > 0 && (
        <ul className="mkt-libgrid">
          {items.map((c) => (
            <CollectionCard key={c.id} c={c} className="mkt-libcard" />
          ))}
        </ul>
      )}
    </section>
  );
}
