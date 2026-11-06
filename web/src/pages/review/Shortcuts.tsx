// The review queue's shortcuts sheet (? opens it).
import { useI18n } from "../../lib/i18n";

const ROWS: Array<[string, string]> = [
  ["J / ↓", "next"],
  ["K / ↑", "prev"],
  ["Space", "play"],
  ["Shift+Space", "slow"],
  ["R", "replay"],
  ["Enter", "accept"],
  ["1–5", "candidate"],
  ["E", "edit"],
  ["N", "name"],
  ["D", "dictionary"],
  ["S", "skip"],
  ["A", "applyAll"],
  ["U, Ctrl+Z", "undo"],
  ["?", "help"],
  ["Esc", "leave"],
];

export function Shortcuts({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  return (
    <div role="dialog" aria-modal="true" aria-labelledby="review-keys" className="mkt-review-keys">
      <h2 id="review-keys">{t("review.shortcuts.title")}</h2>
      <dl>
        {ROWS.map(([key, what]) => (
          <div key={what}>
            <dt>
              <kbd dir="ltr">{key}</kbd>
            </dt>
            <dd>{t(`review.shortcuts.${what}`)}</dd>
          </div>
        ))}
      </dl>
      <button type="button" onClick={onClose} autoFocus>
        {t("common.close")}
      </button>
    </div>
  );
}
