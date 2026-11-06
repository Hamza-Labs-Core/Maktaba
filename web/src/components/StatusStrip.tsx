// The status strip (Epic 53 story 53.06): one line saying what is wrong, highest priority
// first, with "N more" for the rest and, for admins, Details (System health, or the
// recovery page when the API is down). It reads the home's /status.json (lib/homeStatus);
// a feature an admin turned off is no outage and shows nothing. It can be closed for the
// state it shows and comes back when that changes.
import { useState } from "react";
import { useAuth } from "../lib/auth";
import { useCapabilities } from "../lib/capabilities";
import { stripLines, stripSignature, useHomeStatus } from "../lib/homeStatus";
import { useI18n } from "../lib/i18n";
import { useOnline } from "../lib/useOnline";

export function StatusStrip() {
  const online = useOnline();
  const { t } = useI18n();
  const { user } = useAuth();
  const caps = useCapabilities();
  const status = useHomeStatus();
  const [closed, setClosed] = useState("");
  const [open, setOpen] = useState(false);
  const lines = stripLines(status, {
    online,
    admin: !!user?.is_admin,
    liveTvOff: caps.features.live_tv === "off",
  });
  const sig = stripSignature(lines);
  if (lines.length === 0 || closed === sig) return null;
  const shown = open ? lines : lines.slice(0, 1);
  return (
    <div
      className="mkt-home-offline mkt-status-strip"
      role="status"
      aria-live="polite"
      data-testid={lines[0].key === "offline" ? "offline-banner" : "status-strip"}
    >
      {shown.map((l) => (
        <div key={l.key} className="mkt-status-strip-line" data-strip={l.key}>
          <span>{t(l.text)}</span>
          {l.details && (
            <>
              {" "}
              <a href={l.details}>{t("status.details")}</a>
            </>
          )}
        </div>
      ))}
      {lines.length > 1 && !open && (
        <button type="button" className="mkt-status-strip-more" onClick={() => setOpen(true)}>
          {t("status.more", { count: lines.length - 1 })}
        </button>
      )}
      <button
        type="button"
        className="mkt-status-strip-close"
        aria-label={t("status.close")}
        onClick={() => setClosed(sig)}
      >
        ×
      </button>
    </div>
  );
}
