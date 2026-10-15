// Arrow between two queue cards, labelled with the jobs/min that flowed
// along it over the last 10 min. Mirrors under [dir="rtl"] via CSS.
import { useI18n } from "../../lib/i18n";
import { useFmt } from "./format";

export function FlowArrow({
  perMin,
  fromLabel,
  toLabel,
}: {
  perMin: number | null;
  fromLabel: string;
  toLabel: string;
}) {
  const { t } = useI18n();
  const fmt = useFmt();
  const active = perMin !== null && perMin > 0;
  const text = active ? fmt.rate(perMin) : "";
  return (
    <div
      className={`mkt-q-arrow${active ? " mkt-q-arrow--active" : ""}`}
      role="img"
      aria-label={t("queue.flow.aria", {
        from: fromLabel,
        to: toLabel,
        rate: active ? text : t("queue.flow.none"),
      })}
    >
      <span className="mkt-q-arrow__label" aria-hidden="true">
        {text}
      </span>
      <svg className="mkt-q-arrow__svg" viewBox="0 0 40 12" aria-hidden="true">
        <path
          d="M1 6 H33 M27 1.5 L34 6 L27 10.5"
          fill="none"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
    </div>
  );
}
