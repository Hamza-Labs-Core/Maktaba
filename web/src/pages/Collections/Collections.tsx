// Epic 45 — a library's collections live on its page's Collections tab
// (`/library/:libraryId?tab=collections`, story 45.24). This keeps:
//
//   CollectionsRedirect: the old `/library/:libraryId/collections` page's
//     address, sent on to the tab (links and bookmarks keep working);
//   NewCollectionForm: the admins' New collection (an empty list, then opened);
//     a smart one is saved from the Browse tab's filters (Save as collection).
import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../lib/i18n";
import { COLLECTION_NAME_MAX, collectionPath, createCollection } from "../../lib/collections";
import { libraryTabPath } from "../../lib/libraryTabs";
import { errorText } from "../../lib/errorText";

/** `/library/:libraryId/collections`: the library's Collections tab now. */
export function CollectionsRedirect() {
  const { libraryId = "" } = useParams();
  return <Navigate to={libraryTabPath(libraryId, "collections")} replace />;
}

/** An admin's New collection: names an empty list in the library and opens it. */
export function NewCollectionForm({ libraryId }: { libraryId: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const navigate = useNavigate();
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

  return (
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
  );
}
