// The status strip (Epic 53 story 53.06): one line saying what is wrong, highest priority
// first, with "N more" for the rest and, for admins, Details (System health, or the
// recovery page when the API is down). It reads the home's /status.json (lib/homeStatus);
// a feature an admin turned off is no outage and shows nothing. It can be closed for the
// state it shows and comes back when that changes. When the app reaches the home through
// Maktaba Cloud it also reads the cloud's status (lib/cloudStatus, story 50.11): a line after
// the home's own, linking to status.maktaba.tv, closed per incident.
import { useState } from "react";
import { useAuth } from "../lib/auth";
import { cloudLine, throughCloud, useCloudStatus } from "../lib/cloudStatus";
import { useReconnecting } from "../lib/reconnect";
import { useCapabilities } from "../lib/capabilities";
import { stripLines, stripSignature, useHomeStatus } from "../lib/homeStatus";
import { useI18n } from "../lib/i18n";
import { useOnline } from "../lib/useOnline";

export function StatusStrip({ viaCloud = throughCloud() }: { viaCloud?: boolean } = {}) {
  const online = useOnline();
  const { t, formatDate } = useI18n();
  const cloud = useCloudStatus(viaCloud);
  const reconnecting = useReconnecting();
  const { user } = useAuth();
  const caps = useCapabilities();
  const status = useHomeStatus();
  const [closed, setClosed] = useState("");
  const [open, setOpen] = useState(false);
  const lines = stripLines(status, {
    online,
    admin: !!user?.is_admin,
    liveTvOff: caps.features.live_tv === "off",
    reconnecting,
    cloud: cloudLine(cloud, Date.now(), {
      date: (d) => formatDate(d, { dateStyle: "medium" }),
      time: (d) => formatDate(d, { timeStyle: "short" }),
    }),
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
          <span>{t(l.text, l.vars)}</span>
          {(l.details || l.href) && (
            <>
              {" "}
              <a
                href={l.details ?? l.href}
                {...(l.href && !l.details ? { target: "_blank", rel: "noopener noreferrer" } : {})}
              >
                {t("status.details")}
              </a>
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
