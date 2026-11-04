// Epic 51 (story 51.16 §6): offline files on the Components page.
//
// Download for offline: the admin picks a bundle and the kind of machine the file is for;
// POST /api/components/offline starts building it on this machine, and the dialog reads
// GET /api/components/offline/{name} every OFFLINE_POLL_MS for its progress until the file
// is built. Then a link saves it from the same URL (the browser downloads it; the page
// never holds the file).
//
// Import a file: the admin picks a .tar; its bytes are the body of POST
// /api/components/import, with the upload's progress. What it brought is installed (off);
// the admin turns on what they want. A refusal (wrong version or platform, too large, no
// supervisor) is said in the reason's words.
import { useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Modal } from "@ds/components/Modal/Modal";
import { ProgressBar } from "@ds/components/ProgressBar/ProgressBar";
import { Radio, RadioGroup } from "@ds/components/Choice/Radio";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import {
  OFFLINE_PLATFORMS,
  componentsApi,
  type BundleId,
  type InstallProgress,
} from "../../lib/componentsApi";
import { errorText } from "../../lib/errorText";
import { lookupMessage, useI18n, type Locale } from "../../lib/i18n";
import { useBytes } from "../../components/queue/offline";
import { detailText, joinNames, paramVars } from "./componentWords";

/** How often the dialog asks how far the offline file is. */
export const OFFLINE_POLL_MS = 2_000;

const BUNDLES: BundleId[] = ["full", "core", "worker"];

type T = (key: string, vars?: Record<string, string | number>) => string;

/** A packs refusal (`code` and `params`) in the reason's words, else the error's text. */
export function packsErrorText(t: T, locale: Locale, e: unknown): string {
  if (e instanceof ApiError) {
    const p = e.problem as unknown as { code?: string; params?: Record<string, unknown> };
    const key = `components.reason.${p.code ?? ""}`;
    if (p.code && lookupMessage(locale, key) !== undefined) {
      const text = t(key, paramVars(p.params));
      if (!/\{\w+\}/.test(text)) return text;
    }
  }
  return errorText(e, t);
}

export function OfflineDownload({ basedOn, onClose }: { basedOn: string; onClose: () => void }) {
  const { t, locale } = useI18n();
  const bytes = useBytes();
  const [bundle, setBundle] = useState<BundleId>(
    (BUNDLES as string[]).includes(basedOn) ? (basedOn as BundleId) : "full"
  );
  const [platform, setPlatform] = useState("");
  const [name, setName] = useState<string | null>(null);
  const [progress, setProgress] = useState<InstallProgress | null>(null);
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);

  // while it builds: ask how far it is until it is a file, or it failed
  useEffect(() => {
    if (!name || url) return;
    let alive = true;
    let timer: number | null = null;
    const ask = async () => {
      try {
        const s = await componentsApi.offlineState(name);
        if (!alive) return;
        if (s.ready) {
          setUrl(s.url);
          return;
        }
        setProgress(s.progress);
        timer = window.setTimeout(() => void ask(), OFFLINE_POLL_MS);
      } catch (e) {
        if (alive) setError(e);
      }
    };
    void ask();
    return () => {
      alive = false;
      if (timer !== null) window.clearTimeout(timer);
    };
  }, [name, url]);

  const make = async () => {
    setBusy(true);
    setError(null);
    try {
      const r = await componentsApi.makeOffline({ bundle, ...(platform ? { platform } : {}) });
      setName(r.name);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const building = name !== null && url === null && error === null;
  const detail = detailText(t, locale, progress, bytes);

  return (
    <Modal
      open
      onClose={onClose}
      title={t("components.offline.download")}
      className="mkt-comp-offline"
      footer={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t(name ? "common.close" : "common.cancel")}
          </Button>
          {!name && (
            <Button variant="primary" onClick={() => void make()} loading={busy}>
              {t("components.offline.make")}
            </Button>
          )}
        </>
      }
    >
      <p>{t("components.offline.intro")}</p>
      {!name && (
        <>
          <RadioGroup legend={t("components.offline.bundle")} name="offline-bundle">
            {BUNDLES.map((b) => (
              <Radio
                key={b}
                name="offline-bundle"
                value={b}
                checked={bundle === b}
                onChange={() => setBundle(b)}
                label={t(`components.bundle.${b}`)}
              />
            ))}
          </RadioGroup>
          <Select
            label={t("components.offline.platform")}
            value={platform}
            onChange={(e) => setPlatform(e.target.value)}
            options={[
              { value: "", label: t("components.offline.platform.same") },
              ...OFFLINE_PLATFORMS.map((p) => ({
                value: p,
                label: t(`components.offline.platform.${p}`),
              })),
            ]}
          />
        </>
      )}
      {building && (
        <div data-testid="comp-offline-building">
          <p>{t("components.offline.building")}</p>
          <ProgressBar
            label={t("components.offline.building")}
            value={progress?.percent ?? 0}
            indeterminate={!progress}
          />
          {detail && <p>{detail}</p>}
        </div>
      )}
      {url && name && (
        <p data-testid="comp-offline-ready">
          {t("components.offline.ready")}{" "}
          <a href={url} download={name}>
            {t("components.offline.save")}
          </a>
        </p>
      )}
      {error !== null && <p role="alert">{packsErrorText(t, locale, error)}</p>}
    </Modal>
  );
}

export function OfflineImport({
  titleOf,
  onImported,
}: {
  titleOf?: (id: string) => string;
  onImported?: () => void;
}) {
  const { t, n, locale } = useI18n();
  const toast = useToast();
  const input = useRef<HTMLInputElement>(null);
  const [percent, setPercent] = useState<number | null>(null);
  const [error, setError] = useState<unknown>(null);

  const send = async (file: File) => {
    setError(null);
    setPercent(0);
    try {
      const r = await componentsApi.importFile(file, file.name, (f) =>
        setPercent(Math.round(f * 100))
      );
      const ids = r?.installed ?? [];
      toast.show({
        tone: "success",
        message: ids.length
          ? t("components.offline.imported", {
              components: joinNames(
                locale,
                ids.map((id) => (titleOf ? titleOf(id) : id))
              ),
            })
          : t("components.offline.importedNone"),
      });
      onImported?.();
    } catch (e) {
      setError(e);
    } finally {
      setPercent(null);
      if (input.current) input.current.value = "";
    }
  };

  return (
    <span className="mkt-comp-import">
      <input
        ref={input}
        type="file"
        accept=".tar,application/x-tar"
        hidden
        aria-label={t("components.offline.file")}
        data-testid="comp-import-file"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) void send(f);
        }}
      />
      <Button
        variant="secondary"
        disabled={percent !== null}
        onClick={() => input.current?.click()}
      >
        {t("components.offline.import")}
      </Button>
      {percent !== null && (
        <span role="status">{t("components.offline.importing", { percent: n(percent) })}</span>
      )}
      {/* the error is kept as it came and said in the page's language when shown */}
      {error !== null && (
        <span role="alert" className="mkt-comp-import__error">
          {packsErrorText(t, locale, error)}
        </span>
      )}
    </span>
  );
}
