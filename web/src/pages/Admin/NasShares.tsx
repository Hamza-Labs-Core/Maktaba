// Epic 59 story 59.13: a NAS home's own folders and the SMB share each one is. A node opens
// the NAS's files over SMB (/Volumes/Movies on a Mac) and matches them by the share; here
// the admin confirms or changes each folder's share, names the ones setup could not work
// out, or marks one as not shared on the network.
import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { Input } from "@ds/components/Input/Input";
import { useToast } from "@ds/components/Toast/Toast";
import { ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { clusterApi, type HomeShares } from "../../lib/clusterApi";

interface Row {
  path: string;
  share: string;
  localOnly: boolean;
  hostPath: boolean;
}

function rowsOf(hs: HomeShares): Row[] {
  return [
    ...hs.shares.map((s) => ({
      path: s.path,
      share: s.share,
      localOnly: s.local_only,
      hostPath: s.host_path,
    })),
    ...hs.unnamed.map((path) => ({ path, share: "", localOnly: false, hostPath: true })),
  ];
}

/** The NAS's shares card on the Machines page; nothing on a home that is not a NAS. */
export function NasShares({
  load = clusterApi.getHomeShares,
}: {
  load?: () => Promise<HomeShares>;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const showError = (e: unknown) =>
    toast.show({
      tone: "error",
      message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
    });
  const [hs, setHs] = useState<HomeShares | null>(null);
  const [rows, setRows] = useState<Row[]>([]);
  const [busy, setBusy] = useState(false);
  const refresh = useCallback(
    () =>
      load()
        .then((r) => {
          setHs(r);
          setRows(rowsOf(r));
        })
        .catch(() => setHs(null)),
    [load]
  );
  useEffect(() => {
    void refresh();
  }, [refresh]);
  if (!hs?.nas) return null;

  const set = (i: number, patch: Partial<Row>) =>
    setRows(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const missing = rows.some((r) => !r.localOnly && !r.share.trim());
  const save = (e: FormEvent) => {
    e.preventDefault();
    if (missing) return;
    setBusy(true);
    clusterApi
      .putHomeShares({
        addresses: hs.addresses,
        shares: rows.map((r) => ({ path: r.path, share: r.localOnly ? "" : r.share.trim() })),
      })
      .then((r) => {
        setHs(r);
        setRows(rowsOf(r));
      })
      .catch(showError)
      .finally(() => setBusy(false));
  };

  return (
    <section className="mkt-mc-sub" aria-label={t("machines.nasShares.title")}>
      <h4 className="mkt-mc-sub__title">{t("machines.nasShares.title")}</h4>
      <p className="mkt-mc-muted">
        {t("machines.nasShares.intro", { server: hs.server })}{" "}
        <bdi dir="ltr">{hs.addresses.join(", ")}</bdi>
      </p>
      {hs.notices.includes("no_writable_share") && (
        <p className="mkt-mc-warn" role="status">
          {t("cluster.no_writable_share")}
        </p>
      )}
      {hs.other_path && <p className="mkt-mc-muted">{t("settings.share.other_path")}</p>}
      <form onSubmit={save} className="mkt-mc-map-form">
        {rows.map((r, i) => (
          <div key={r.path} className="mkt-mc-map-row" data-testid="nas-share">
            <bdi dir="ltr">
              <code>{r.path}</code>
            </bdi>
            {r.localOnly ? (
              <span className="mkt-mc-muted">{t("setup.share.local_only")}</span>
            ) : (
              <span dir="ltr">
                <Input
                  label={t("machines.nasShares.share", { path: r.path })}
                  value={r.share}
                  onChange={(e) => set(i, { share: e.currentTarget.value })}
                />
                {r.share.trim() && (
                  <code className="mkt-mc-muted">{`smb://${hs.server}/${r.share.trim()}`}</code>
                )}
              </span>
            )}
            <Checkbox
              label={t("setup.share.notShared")}
              checked={r.localOnly}
              onChange={(e) => set(i, { localOnly: e.target.checked })}
            />
          </div>
        ))}
        <div className="mkt-mc-actions">
          <Button size="sm" type="submit" loading={busy} disabled={missing}>
            {t("common.save")}
          </Button>
        </div>
      </form>
    </section>
  );
}
