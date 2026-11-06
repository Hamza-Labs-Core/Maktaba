// Epic 56 story 56.13 (and 56.11 on the TVs): Home offers to continue the play queue
// the user last used, on any device, when it was used in the last 24 hours:
// "{title} · from {device}". Select plays its current video from where it was.
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../lib/i18n";
import {
  currentItem,
  fetchCurrentQueue,
  resumeQueue,
  watchPath,
  type ServerQueue,
} from "../lib/playQueue";

/** How recently a queue was used for Home to offer it. */
export const CONTINUE_QUEUE_WITHIN_MS = 24 * 60 * 60 * 1000;

/** A device id as words: the web's own, a TV, a phone, else the id as it is. */
export function deviceLabel(id: string | undefined, t: (k: string) => string): string {
  if (!id) return "";
  if (id.startsWith("web")) return t("devices.kind.web");
  if (id.startsWith("tv")) return t("devices.kind.tv");
  if (id.startsWith("phone") || id.startsWith("android") || id.startsWith("ios")) return t("devices.kind.phone");
  return id;
}

export function ContinueQueue({ now = () => Date.now() }: { now?: () => number }) {
  const { t } = useI18n();
  const navigate = useNavigate();
  const [q, setQ] = useState<ServerQueue | null>(null);
  useEffect(() => {
    let live = true;
    fetchCurrentQueue()
      .then((x) => {
        if (!live || !x) return;
        const used = x.used_at ? Date.parse(x.used_at) : NaN;
        if (Number.isFinite(used) && now() - used <= CONTINUE_QUEUE_WITHIN_MS && currentItem(x)) setQ(x);
      })
      .catch(() => {
        /* an older server, or none: no row */
      });
    return () => {
      live = false;
    };
  }, [now]);
  const cur = q ? currentItem(q) : null;
  if (!q || !cur) return null;
  const title = cur.title || cur.filename || "";
  const device = deviceLabel(q.device_id, t);
  return (
    <section className="mkt-continue-queue" aria-label={t("home.continueQueue")} data-testid="row-continue-queue">
      <h2 className="mkt-row__title">{t("home.continueQueue")}</h2>
      <Button
        variant="secondary"
        onClick={() => {
          const id = resumeQueue(q);
          if (id) {
            const at = Math.floor(cur.position_sec ?? 0);
            navigate(`${watchPath(id)}${at > 0 ? `?t=${at}` : ""}`, { state: { autoplay: true } });
          }
        }}
      >
        ▶{" "}
        <bdi>{device ? t("home.continueQueue.from", { title, device }) : title}</bdi>
      </Button>
    </section>
  );
}
