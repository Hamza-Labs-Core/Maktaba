// Epic 51 (story 51.13 §5): the dialog that asks before a component's settings go back to
// the defaults. It counts the settings that would be lost from GET /api/rules (the fields
// marked changed in the sections the component owns), then POST
// /api/components/{id}/settings/reset deletes those sections. Turning a component off or
// removing it never touches its settings; only this does.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { useToast } from "@ds/components/Toast/Toast";
import { componentsApi, refusalOf } from "../../lib/componentsApi";
import { rulesApi } from "../../lib/rulesApi";
import { errorText } from "../../lib/errorText";
import { useI18n } from "../../lib/i18n";

export function ComponentResetSettings({
  id,
  name,
  onClose,
}: {
  id: string;
  name: string;
  onClose: () => void;
}) {
  const { t, n } = useI18n();
  const toast = useToast();
  const [changed, setChanged] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    rulesApi
      .list()
      .then((r) => {
        if (!alive) return;
        const owned = r.sections.filter((s) => s.component === id);
        setChanged(owned.reduce((sum, s) => sum + s.fields.filter((f) => f.changed).length, 0));
      })
      .catch((e: unknown) => alive && setError(errorText(e, t)));
    return () => {
      alive = false;
    };
  }, [id, t]);

  const reset = async () => {
    setBusy(true);
    try {
      await componentsApi.resetSettings(id);
      toast.show({
        tone: "success",
        message: t("components.settings.resetDone", { component: name }),
      });
      onClose();
    } catch (e) {
      const r = refusalOf(e);
      const key = r ? `components.refused.${r.code}` : "";
      const said = key ? t(key, { name }) : key;
      setError(said && said !== key ? said : errorText(e, t));
      setBusy(false);
    }
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={t("components.settings.resetTitle", { component: name })}
      className="mkt-comp-reset"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button
            variant="destructive"
            onClick={() => void reset()}
            loading={busy}
            disabled={changed === null && error === null}
          >
            {t("components.settings.reset")}
          </Button>
        </>
      }
    >
      {changed === null && error === null && <p aria-busy="true">…</p>}
      {changed !== null && (
        <p data-testid="comp-reset-text">
          {changed > 0
            ? t("components.settings.resetConfirm", { component: name, n: n(changed) })
            : t("components.settings.resetNone", { component: name })}
        </p>
      )}
      {error && <p role="alert">{error}</p>}
    </Modal>
  );
}
