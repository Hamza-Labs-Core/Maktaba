// The Queues page's notice that the machine the server runs on is overloaded (the `host`
// block of GET /api/queues/pulse): more work wants the CPU than it has cores, so jobs and
// the database are slow, often because of a program outside Maktaba. It names the
// programs using the most CPU when the server could list them. It explains only: Maktaba
// stops no program.
import { bidiIsolate, useI18n } from "../../lib/i18n";
import { numberFormat } from "../../lib/intl";
import type { HostLoad } from "../../lib/queueApi";

export function HostOverloaded({ host }: { host: HostLoad }) {
  const { t, n, locale } = useI18n();
  const num = (v: number) =>
    numberFormat(locale === "ar" ? "ar-EG" : "en", { maximumFractionDigits: 1 }).format(v);
  const top = host.top ?? [];
  const sep = locale === "ar" ? "، " : ", ";
  return (
    <div className="mkt-q-banner mkt-q-banner--warn mkt-q-overload" role="alert">
      <strong>{t("queue.hostOverloaded.title")}</strong>
      <p>
        {t("queue.hostOverloaded.load", {
          load: num(host.load1),
          load5: num(host.load5),
          cores: n(host.cores),
        })}
      </p>
      {top.length > 0 && (
        <>
          <p>
            {t("queue.hostOverloaded.top", {
              programs: top
                .map((p) =>
                  t("queue.hostOverloaded.proc", {
                    name: bidiIsolate(p.name),
                    pct: n(Math.round(p.cpu_pct)),
                  })
                )
                .join(sep),
            })}
          </p>
          <p>{t("queue.hostOverloaded.hint")}</p>
        </>
      )}
    </div>
  );
}
