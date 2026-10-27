// The permissions the Mac server needs, one card each: why, whether it is on,
// "Open System Settings" (the exact pane) and the steps. Shared by the
// setup's Permissions step and the reminder in this Mac's own web app.
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import {
  isMissing,
  type Grant,
  type PermissionItem,
  type PermissionsApi,
  type PermissionsReport,
} from "../../lib/macPermissions";

const TONE: Record<Grant, "success" | "error" | "warn" | "neutral"> = {
  granted: "success",
  denied: "error",
  not_asked: "warn",
  unknown: "neutral",
};

/** The report, read now and again whenever the window comes back (the user
 * returns from System Settings). null: not read yet, or the shell cannot say. */
export function usePermissions(api: PermissionsApi, enabled = true) {
  const [report, setReport] = useState<PermissionsReport | null>(null);
  const [failed, setFailed] = useState(false);
  const alive = useRef(true);
  const refresh = useCallback(() => {
    if (!enabled) return;
    api
      .report()
      .then((r) => {
        if (!alive.current) return;
        setReport(r);
        setFailed(false);
      })
      .catch(() => alive.current && setFailed(true));
  }, [api, enabled]);
  useEffect(() => {
    alive.current = true;
    refresh();
    const onVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      alive.current = false;
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [refresh]);
  return { report, failed, refresh };
}

/** The numbered steps of an item (perm.item.<id>.step1 …), as many as the catalog has. */
function steps(t: (k: string) => string, id: string): string[] {
  const out: string[] = [];
  for (let n = 1; n <= 6; n++) {
    const k = `perm.item.${id}.step${n}`;
    const s = t(k);
    if (s === k) break;
    out.push(s);
  }
  return out;
}

function stateText(t: (k: string) => string, it: PermissionItem): string {
  const own = `perm.item.${it.id}.state.${it.state}`;
  const s = t(own);
  return s === own ? t(`perm.state.${it.state}`) : s;
}

function Card({ it, api }: { it: PermissionItem; api: PermissionsApi }) {
  const { t } = useI18n();
  const [openFailed, setOpenFailed] = useState(false);
  const missing = isMissing(it);
  // what happens to an item not asked yet (the login item: setup's last step)
  const noteKey = `perm.item.${it.id}.notAsked`;
  const note = it.state === "not_asked" && t(noteKey) !== noteKey ? t(noteKey) : null;
  return (
    <li
      className={missing ? "mkt-perm mkt-perm--missing" : "mkt-perm"}
      data-testid={`perm-${it.id}`}
      data-state={it.state}
    >
      <div className="mkt-perm__head">
        <h3 className="mkt-perm__title">{t(`perm.item.${it.id}.title`)}</h3>
        <span className="mkt-perm__badges">
          <Badge tone={it.required ? "accent" : "neutral"}>
            {t(it.required ? "perm.required" : "perm.optional")}
          </Badge>
          <Badge tone={TONE[it.state]} data-testid={`perm-${it.id}-state`}>
            {stateText(t, it)}
          </Badge>
        </span>
      </div>
      <p>{t(`perm.item.${it.id}.why`)}</p>
      {note && <p className="mkt-perm__note">{note}</p>}
      {it.state !== "granted" && (
        <>
          <ol className="mkt-perm__steps">
            {steps(t, it.id).map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ol>
          <Button
            type="button"
            variant={missing ? "primary" : "secondary"}
            onClick={() => {
              setOpenFailed(false);
              api.openPane(it.pane).catch(() => setOpenFailed(true));
            }}
          >
            {t("perm.open")}
          </Button>
          {openFailed && (
            <p className="mkt-setup__warn" role="alert">
              {t("perm.openFailed")}
            </p>
          )}
        </>
      )}
    </li>
  );
}

export function PermissionList({
  report,
  api,
  onRecheck,
}: {
  report: PermissionsReport;
  api: PermissionsApi;
  onRecheck: () => void;
}) {
  const { t } = useI18n();
  return (
    <div className="mkt-perms">
      <p>{t("perm.intro")}</p>
      <ul className="mkt-perms__list">
        {report.items.map((it) => (
          <Card key={it.id} it={it} api={api} />
        ))}
      </ul>
      <Button type="button" variant="ghost" onClick={onRecheck}>
        {t("perm.recheck")}
      </Button>
    </div>
  );
}
