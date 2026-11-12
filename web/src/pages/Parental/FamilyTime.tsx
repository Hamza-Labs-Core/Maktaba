// Epic 57 story 57.15: the restricted accounts, each with its way to the time
// rules (Users > the user > Time). The Family page (Family.tsx) lists them with
// everything else a guardian sets.
import { Link } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { useI18n } from "../../lib/i18n";
import { tierKey, type Child } from "../../lib/parentalApi";
import { timePath } from "./Child";
import "../../styles/parental.css";

export { timePath };

/** The list itself: also shown under Users for an admin. */
export function ChildrenTime({ accounts }: { accounts: Child[] }) {
  const { t, locale } = useI18n();
  const sorted = [...accounts].sort((a, b) => a.username.localeCompare(b.username, locale));
  return (
    <ul className="mkt-more__list" data-testid="parental-children">
      {sorted.map((c) => (
        <li key={c.id}>
          <Link className="mkt-more__link" to={timePath(c.id)}>
            <bdi>{c.username || c.id}</bdi>
            {c.maxTier !== null && (
              <Badge tone="neutral">
                <bdi>{t(tierKey(c.maxTier))}</bdi>
              </Badge>
            )}
            <span>{t("parental.time.rules")}</span>
          </Link>
        </li>
      ))}
    </ul>
  );
}
