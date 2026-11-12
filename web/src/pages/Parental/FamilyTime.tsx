// Epic 57 story 57.15: the restricted accounts this person looks after, each with
// its way to the time rules (Users > the user > Time).
//
//   GET /api/parental/children   an admin: every restricted account; a guardian:
//                                their own; anyone else: none
//
// The page shows what the home answers. A viewer who looks after no one gets the
// empty state here, and 403 from the home on every route behind it.
import { Link } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Badge } from "@ds/components/Badge/Badge";
import { useI18n } from "../../lib/i18n";
import { fetchChildren, tierKey, type Child } from "../../lib/parentalApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { TitleRow } from "../../help/HelpLink";
import "../../styles/parental.css";

export const timePath = (userId: string) => `/family/${encodeURIComponent(userId)}/time`;

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

export function FamilyTime() {
  const { t } = useI18n();
  const { data, err, loading, reload } = usePageData("parental:children", fetchChildren);
  return (
    <section className="mkt-page">
      <TitleRow>
        <h1>{t("parental.family.title")}</h1>
      </TitleRow>
      <p className="mkt-parental-note">{t("parental.family.intro")}</p>
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} />}
      {data && data.length === 0 && (
        <EmptyState
          kind="first_run"
          title={t("parental.family.empty.title")}
          description={t("parental.family.empty.desc")}
        />
      )}
      {data && data.length > 0 && <ChildrenTime accounts={data} />}
    </section>
  );
}
