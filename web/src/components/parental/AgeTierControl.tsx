// Epic 57 story 57.01: the age rating an admin gives a video, a series, a
// collection, a channel, a YouTube source, or a library (its default).
//
//   PUT /api/{videos,libraries,series,collections,channels}/{id}/age-tier {tier | null}
//   PUT /api/libraries/{id}/youtube/sources/{sid}/age-tier                {tier | null}
//     → {tier, source, recompute_count}
//
// Admins only, and the home refuses anyone else (403). No route of the home reads a
// container's rating back, so the control starts on "choose" unless the page knows
// the rating (`current`), and shows what the home answered after a save.
import { useId, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { ApiError } from "../../lib/api";
import { useOptionalAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import {
  TIERS,
  setAgeTier,
  tierKey,
  type Tier,
  type TierAnswer,
  type TierTarget,
} from "../../lib/parentalApi";
import "../../styles/parental.css";

/** The select's values: a tier, "none" (clear the admin's rating) or "" (not chosen). */
type Choice = `${Tier}` | "none" | "";

export function choiceOf(tier: number | null | undefined): Choice {
  if (tier === undefined) return "";
  if (tier === null) return "none";
  return (TIERS as readonly number[]).includes(tier) ? (`${tier}` as Choice) : "";
}

export function tierOf(choice: Choice): Tier | null {
  return choice === "none" || choice === "" ? null : (Number(choice) as Tier);
}

/** The tier picker's options, youngest first in both languages. */
export function TierOptions({ clearLabel }: { clearLabel: string }) {
  const { t } = useI18n();
  return (
    <>
      {TIERS.map((tier) => (
        <option key={tier} value={String(tier)}>
          {t(tierKey(tier))}
        </option>
      ))}
      <option value="none">{clearLabel}</option>
    </>
  );
}

export interface AgeTierControlProps {
  target: TierTarget;
  /** the rating the page knows it has (null: none); leave out when it does not know */
  current?: number | null;
  /** the field's label (default: Age rating) */
  label?: string;
  onSaved?: (answer: TierAnswer) => void;
}

export function AgeTierControl({ target, current, label, onSaved }: AgeTierControlProps) {
  const user = useOptionalAuth()?.user;
  const { t, n } = useI18n();
  const id = useId();
  const [choice, setChoice] = useState<Choice>(choiceOf(current));
  const [busy, setBusy] = useState(false);
  const [answer, setAnswer] = useState<TierAnswer | null>(null);
  const [error, setError] = useState<string | null>(null);
  // the home refuses anyone else; nothing to show them
  if (!user?.is_admin) return null;

  async function save() {
    if (choice === "") return;
    setBusy(true);
    setError(null);
    try {
      const a = await setAgeTier(target, tierOf(choice));
      setAnswer(a);
      onSaved?.(a);
    } catch (e) {
      setAnswer(null);
      setError(
        e instanceof ApiError && e.status === 403
          ? t("parental.tier.error.forbidden")
          : e instanceof ApiError && e.status === 404
            ? t("parental.tier.error.notFound")
            : t("common.error")
      );
    } finally {
      setBusy(false);
    }
  }

  const isLibrary = target.kind === "library";
  return (
    <div className="mkt-age-tier" data-testid="age-tier">
      <label className="mkt-field" htmlFor={id}>
        <span className="mkt-field__label">
          {label ?? t(isLibrary ? "parental.tier.libraryDefault" : "parental.tier.label")}
        </span>
        <select id={id} value={choice} onChange={(e) => setChoice(e.target.value as Choice)}>
          {choice === "" && <option value="">{t("parental.tier.choose")}</option>}
          <TierOptions clearLabel={t("parental.tier.clear")} />
        </select>
      </label>
      <Button
        variant="secondary"
        disabled={choice === "" || busy}
        loading={busy}
        onClick={() => void save()}
      >
        {t("parental.tier.set")}
      </Button>
      {answer && (
        <p className="mkt-age-tier__result" role="status">
          {target.kind === "video"
            ? t("parental.tier.nowVideo", { tier: t(tierKey(answer.tier)) })
            : t("parental.tier.saved", { count: n(answer.recomputeCount) })}
        </p>
      )}
      {error && (
        <p className="mkt-alert" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
