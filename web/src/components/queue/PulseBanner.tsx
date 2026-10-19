// The heartbeat line under the header: "Last activity 12 s ago", or "No activity
// for 14 min: it may be stuck", with the jobs that stopped beating
// (GET /api/queues/pulse, polled; the stand-alone centre's stuck detection).
import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { queueApi } from "../../lib/queueApi";
import type { Pulse } from "../../lib/queueApi";
import { jobHref } from "./RunningJobs";
import { useFmt } from "./format";

const POLL_MS = 5_000;

export function PulseBanner({ pollMs = POLL_MS }: { pollMs?: number }) {
  const { t } = useI18n();
  const fmt = useFmt();
  const [pulse, setPulse] = useState<Pulse | null>(null);

  useEffect(() => {
    let alive = true;
    const load = () =>
      queueApi
        .pulse()
        .then((r) => alive && setPulse(r))
        .catch(() => {});
    void load();
    const id = setInterval(load, pollMs);
    return () => {
      alive = false;
      clearInterval(id);
    };
  }, [pollMs]);

  if (!pulse) return null;
  const quiet = pulse.state === "idle";
  const age = pulse.age_sec;
  let text: string;
  if (pulse.state === "paused") text = t("queue.pulse.paused");
  else if (quiet) text = t("queue.pulse.idle");
  else if (age === null) text = t("queue.pulse.noActivity");
  else if (pulse.state === "stalled") text = t("queue.pulse.stalled", { time: fmt.dur(age) });
  else text = t("queue.pulse.last", { time: fmt.dur(age) });

  return (
    <div
      className={`mkt-q-pulse mkt-q-pulse--${pulse.state}`}
      role={pulse.state === "stalled" ? "alert" : undefined}
    >
      <span className="mkt-q-pulse__dot" aria-hidden="true" />
      <span>{text}</span>
      {(pulse.stuck ?? []).length > 0 && (
        <ul className="mkt-q-pulse__stuck">
          {(pulse.stuck ?? []).map((j) => (
            <li key={j.job_id}>
              <Link to={jobHref(j)}>{bidiIsolate(j.label)}</Link>{" "}
              <span className="mkt-q-muted">
                {t("queue.pulse.stuckJob", {
                  stage: j.stage,
                  time: fmt.dur(j.heartbeat_age_sec),
                })}
              </span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
