// Epic 51 (story 51.17): Settings → Components → Change bundle. The admin picks Full, Core
// or Worker machine; the dialog shows the difference (POST /api/components/bundle with
// dry_run: what turns on, what downloads and how much, what turns off, what loses its
// files) and applies it as one batch with the revision it showed.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Radio, RadioGroup } from "@ds/components/Choice/Radio";
import { Modal } from "@ds/components/Modal/Modal";
import { componentsApi, type BundleDiff, type BundleId } from "../../lib/componentsApi";
import { formatBytes } from "../../lib/desktopSetup";
import { errorText } from "../../lib/errorText";
import { useI18n } from "../../lib/i18n";
import { joinNames } from "./componentWords";

const BUNDLES: BundleId[] = ["full", "core", "worker"];

export function BundleDialog({
  basedOn,
  titleOf,
  onClose,
  onApplied,
}: {
  basedOn: string;
  titleOf: (id: string) => string;
  onClose: () => void;
  onApplied: (bundle: BundleId) => void;
}) {
  const { t, locale } = useI18n();
  const [to, setTo] = useState<BundleId>(basedOn === "full" ? "core" : "full");
  const [removeUnused, setRemoveUnused] = useState(false);
  const [diff, setDiff] = useState<BundleDiff | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    let alive = true;
    setDiff(null);
    setError(null);
    componentsApi
      .bundle({ bundle: to, dry_run: true, remove_unused: removeUnused })
      .then((d) => alive && setDiff(d))
      .catch((e: unknown) => alive && setError(e));
    return () => {
      alive = false;
    };
  }, [to, removeUnused]);
  const names = (ids: string[]) => joinNames(locale, ids.map(titleOf));
  const nothing =
    diff &&
    !diff.enable.length &&
    !diff.install.length &&
    !diff.disable.length &&
    !diff.remove.length;
  const refused = diff?.effect.refused ?? [];
  const apply = async () => {
    if (!diff) return;
    setBusy(true);
    try {
      await componentsApi.bundle({
        bundle: to,
        remove_unused: removeUnused,
        generation: diff.revision,
      });
      onApplied(to);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={t("components.bundle.dialogTitle", { bundle: t(`components.bundle.${to}`) })}
      className="mkt-comp-confirm"
      footer={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button
            variant={diff?.remove.length ? "destructive" : "primary"}
            onClick={() => void apply()}
            loading={busy}
            disabled={!diff || !!nothing || refused.length > 0}
          >
            {t("components.bundle.apply")}
          </Button>
        </>
      }
    >
      <RadioGroup legend={t("components.bundle.pick")} name="mkt-comp-bundle">
        {BUNDLES.map((b) => (
          <Radio
            key={b}
            name="mkt-comp-bundle"
            value={b}
            checked={to === b}
            onChange={() => setTo(b)}
            label={t(`components.bundle.${b}`)}
          />
        ))}
      </RadioGroup>
      <Checkbox
        label={t("components.bundle.removeUnused")}
        checked={removeUnused}
        onChange={(e) => setRemoveUnused(e.target.checked)}
      />
      {error !== null && <p role="alert">{errorText(error, t)}</p>}
      {diff && (
        <div data-testid="comp-bundle-diff">
          {nothing && <p>{t("components.bundle.nothing")}</p>}
          {diff.enable.length > 0 && (
            <p>{t("components.bundle.enable", { components: names(diff.enable) })}</p>
          )}
          {diff.install.length > 0 && (
            <p>
              {t("components.bundle.install", {
                components: names(diff.install.map((a) => a.id)),
                size: formatBytes(diff.download_bytes),
              })}
            </p>
          )}
          {diff.disable.length > 0 && (
            <p>{t("components.bundle.disable", { components: names(diff.disable) })}</p>
          )}
          {diff.remove.length > 0 && (
            <p>{t("components.bundle.remove", { components: names(diff.remove) })}</p>
          )}
          {refused.map((r, i) => (
            <p key={`${r.code}-${i}`} role="alert">
              {r.code === "licence_missing" ? t("components.licence.refused") : r.detail}
            </p>
          ))}
        </div>
      )}
    </Modal>
  );
}
