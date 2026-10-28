// Epic 45 Phase 3 (story 45.17) — admins: "Add to collection", from a
// selection on a library's grid. Lists the library's manual collections (each
// with an Add button) and a field to make a new one with the videos in it.
import { useEffect, useState } from "react";
import { Modal } from "@ds/components/Modal/Modal";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../lib/i18n";
import { useOptionalAuth } from "../lib/auth";
import { cachedAnswer, keepAnswer } from "../lib/pageCache";
import { LinesPlaceholder, Loading } from "./Placeholders";
import {
  addToCollection,
  COLLECTION_NAME_MAX,
  createCollection,
  fetchCollections,
  type Collection,
} from "../lib/collections";
import { errorText } from "../lib/errorText";

export function AddToCollection({
  open,
  libraryId,
  videoIds,
  onClose,
  onAdded,
}: {
  open: boolean;
  libraryId: string;
  videoIds: string[];
  onClose: () => void;
  onAdded?: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [lists, setLists] = useState<Collection[] | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const userId = useOptionalAuth()?.user?.id ?? null;

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    // the library's collections its page kept show at once, read again behind them
    const key = `library-collections:${libraryId}`;
    const kept = cachedAnswer<Collection[]>(userId, key);
    setLists(kept ? kept.filter((c) => !c.is_smart) : null);
    setLoadErr(null);
    setName("");
    fetchCollections(libraryId)
      .then((cs) => {
        if (cancelled) return;
        keepAnswer(userId, key, cs);
        setLists(cs.filter((c) => !c.is_smart));
      })
      .catch((e) => {
        if (cancelled) return;
        setLists([]);
        setLoadErr(errorText(e, t));
      });
    return () => {
      cancelled = true;
    };
  }, [open, libraryId, t, userId]);

  async function addTo(c: { id: string; name: string }) {
    setBusy(true);
    try {
      const added = await addToCollection(c.id, videoIds);
      toast.show({
        tone: "success",
        message: t("collections.added", { n: n(added), name: c.name }),
      });
      onAdded?.();
      onClose();
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("collections.addError")}: ${errorText(e, t)}`,
      });
    } finally {
      setBusy(false);
    }
  }

  async function createWith() {
    const nm = name.trim();
    if (!nm) return;
    setBusy(true);
    try {
      const c = await createCollection(libraryId, nm);
      setBusy(false);
      await addTo(c);
    } catch (e) {
      toast.show({
        tone: "error",
        message: `${t("collections.createError")}: ${errorText(e, t)}`,
      });
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={t("collections.addTitle", { n: n(videoIds.length) })}
      footer={
        <Button variant="ghost" onClick={onClose}>
          {t("common.cancel")}
        </Button>
      }
    >
      <div className="mkt-addpl">
        <Loading loading={lists === null}>
          <LinesPlaceholder lines={3} />
        </Loading>
        {loadErr && <p className="mkt-muted">{loadErr}</p>}
        {lists && lists.length > 0 && (
          <ul className="mkt-addpl__list" aria-label={t("collections.title")}>
            {lists.map((c) => (
              <li key={c.id} className="mkt-addpl__item">
                <bdi className="mkt-addpl__name">{c.name}</bdi>
                <span className="mkt-muted">{t("libraries.videos", { n: n(c.videos ?? 0) })}</span>
                <Button
                  size="sm"
                  variant="secondary"
                  disabled={busy}
                  aria-label={t("collections.addToNamed", { name: c.name })}
                  onClick={() => void addTo(c)}
                >
                  {t("collections.add")}
                </Button>
              </li>
            ))}
          </ul>
        )}
        {lists && lists.length === 0 && !loadErr && (
          <p className="mkt-muted">{t("collections.noneYet")}</p>
        )}
        <form
          className="mkt-addpl__new"
          onSubmit={(e) => {
            e.preventDefault();
            void createWith();
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
          <Button type="submit" disabled={busy || !name.trim()}>
            {t("collections.createWith")}
          </Button>
        </form>
      </div>
    </Modal>
  );
}
