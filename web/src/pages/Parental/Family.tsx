// Epic 57 family settings on the web: the restricted accounts this person looks
// after, the requests that wait, and the parent PIN.
//
//   GET /api/parental/children   an admin: every restricted account; a guardian:
//                                their own; anyone else: none
//   GET /api/admin/library-acl   the home's accounts (an admin picks the one to restrict)
//
// A home with no iPhone manages everything here. The page shows what the home
// answers: a viewer who looks after no one gets the empty state, and 403 from the
// home on every route behind it. Only an admin makes an account restricted.
import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { useOptionalAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { useMyParental } from "../../lib/parental";
import { fetchChildren, tierKey, type Child } from "../../lib/parentalApi";
import { fetchAccounts, type Person } from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { TitleRow } from "../../help/HelpLink";
import { childPath } from "./Child";
import { HomeSwitch } from "./HomeSwitch";
import "../../styles/parental.css";

interface Loaded {
  children: Child[];
  /** the home's accounts (an admin only) */
  accounts: Person[];
}

export function Family() {
  const { t, n, locale } = useI18n();
  const me = useOptionalAuth()?.user;
  const admin = me?.is_admin === true;
  const { pendingRequests } = useMyParental();
  const { data, err, loading, reload } = usePageData<Loaded>(
    "parental:family",
    async () => {
      const children = await fetchChildren();
      const accounts = admin ? await fetchAccounts().catch(() => []) : [];
      return { children, accounts };
    },
    [admin]
  );
  const sorted = [...(data?.children ?? [])].sort((a, b) =>
    a.username.localeCompare(b.username, locale)
  );
  const manages = admin || sorted.length > 0;
  return (
    <section className="mkt-page">
      <TitleRow>
        <h1>{t("family.title")}</h1>
      </TitleRow>
      <p className="mkt-parental-note">{t("family.intro")}</p>
      {admin && <HomeSwitch />}
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} />}
      {data && manages && (
        <ul className="mkt-more__list" data-testid="family-links">
          <li>
            <Link className="mkt-more__link" to="/family/requests">
              {t("family.requests")}
              {pendingRequests > 0 && (
                <span
                  className="mkt-nav-count"
                  data-testid="family-requests-count"
                  aria-label={t("family.requests.waiting", { count: n(pendingRequests) })}
                >
                  {pendingRequests > 99 ? "99+" : n(pendingRequests)}
                </span>
              )}
            </Link>
          </li>
          <li>
            <Link className="mkt-more__link" to="/family/pin">
              {t("family.pin")}
            </Link>
          </li>
        </ul>
      )}
      {data && (
        <>
          <h2>{t("family.children")}</h2>
          {sorted.length === 0 ? (
            <EmptyState
              kind="first_run"
              title={t("parental.family.empty.title")}
              description={t(admin ? "family.empty.admin" : "parental.family.empty.desc")}
            />
          ) : (
            <ul className="mkt-more__list" data-testid="parental-children">
              {sorted.map((c) => (
                <li key={c.id}>
                  <Link className="mkt-more__link" to={childPath(c.id)}>
                    <bdi>{c.username || c.id}</bdi>
                    {c.maxTier !== null && (
                      <Badge tone="neutral">
                        <bdi>{t(tierKey(c.maxTier))}</bdi>
                      </Badge>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {data && admin && (
        <MakeRestricted
          accounts={data.accounts.filter(
            (a) => a.id !== me?.id && !data.children.some((c) => c.id === a.id)
          )}
        />
      )}
    </section>
  );
}

/** An admin picks the account; its page takes the limit (the first PUT). */
function MakeRestricted({ accounts }: { accounts: Person[] }) {
  const { t, locale } = useI18n();
  const navigate = useNavigate();
  const [pick, setPick] = useState("");
  const sorted = [...accounts].sort((a, b) => a.username.localeCompare(b.username, locale));
  return (
    <section aria-labelledby="family-make" data-testid="family-make">
      <h2 id="family-make">{t("family.makeRestricted")}</h2>
      <p className="mkt-parental-note">{t("family.makeRestricted.pick")}</p>
      {sorted.length === 0 ? (
        <p className="mkt-muted">
          {t("family.makeRestricted.none")} <Link to="/admin/users">{t("nav.admin.users")}</Link>
        </p>
      ) : (
        <div className="mkt-parental-row">
          <Select
            label={t("family.accounts.label")}
            value={pick}
            onChange={(e) => setPick(e.target.value)}
          >
            <option value="">{t("family.accounts.choose")}</option>
            {sorted.map((a) => (
              <option key={a.id} value={a.id}>
                {a.username || a.id}
              </option>
            ))}
          </Select>
          <Button variant="secondary" disabled={!pick} onClick={() => navigate(childPath(pick))}>
            {t("family.makeRestricted.next")}
          </Button>
        </div>
      )}
    </section>
  );
}
