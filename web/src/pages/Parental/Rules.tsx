// Epic 57 family settings on the web: what one account is allowed and what is
// blocked for it, beside its limit (Family > the account).
//
//   GET    /api/users/{id}/parental/rules           {rules: [...]}
//   POST   /api/users/{id}/parental/rules           {effect, subject_kind, subject_id, expires_at?}
//   DELETE /api/users/{id}/parental/rules/{rid}
//
// An admin or that account's guardian. A rule holds a kind and an id; its name is
// read in this person's own view (subjects.tsx), so something in a library they
// cannot open shows its kind alone.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../lib/i18n";
import { addRule, fetchRules, removeRule, type Rule, type RuleEffect } from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import {
  ExpirySelect,
  SubjectPicker,
  expiryOf,
  useSubjectNames,
  type ExpiryChoice,
  type Picked,
} from "./subjects";
import { familyError } from "./words";

const EFFECTS: RuleEffect[] = ["allow", "block"];

export function ChildRules({ userId }: { userId: string }) {
  const { t, formatDate } = useI18n();
  const { data, err, loading, reload, setData } = usePageData<Rule[]>(
    `parental:rules:${userId}`,
    () => fetchRules(userId),
    [userId]
  );
  const names = useSubjectNames(data ?? []);
  const [effect, setEffect] = useState<RuleEffect>("allow");
  const [picked, setPicked] = useState<Picked | null>(null);
  const [expiry, setExpiry] = useState<ExpiryChoice>("always");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function add() {
    if (!picked) return;
    setBusy(true);
    setError(null);
    try {
      const made = await addRule(userId, {
        effect,
        subjectKind: picked.kind,
        subjectId: picked.id,
        expiresAt: expiryOf(expiry),
      });
      // the same rule again only changes when it ends: it keeps its id
      setData((prev) => [...(prev ?? []).filter((r) => r.id !== made.id), made]);
      setPicked(null);
    } catch (e) {
      setError(familyError(e, t));
    } finally {
      setBusy(false);
    }
  }

  async function remove(rule: Rule) {
    setBusy(true);
    setError(null);
    try {
      await removeRule(userId, rule.id);
      setData((prev) => (prev ?? []).filter((r) => r.id !== rule.id));
    } catch (e) {
      setError(familyError(e, t));
    } finally {
      setBusy(false);
    }
  }

  const now = Date.now();
  const listOf = (which: RuleEffect) => {
    const rules = (data ?? []).filter((r) => r.effect === which);
    return (
      <section data-testid={`family-rules-${which}`}>
        <h3>{t(which === "allow" ? "family.child.allowed" : "family.child.blocked")}</h3>
        {rules.length === 0 ? (
          <p className="mkt-muted">
            {t(which === "allow" ? "family.rule.noneAllowed" : "family.rule.noneBlocked")}
          </p>
        ) : (
          <ul className="mkt-parental-list">
            {rules.map((r) => {
              const name = names[`${r.subjectKind}:${r.subjectId}`];
              const ended = r.expiresAt !== null && Date.parse(r.expiresAt) <= now;
              return (
                <li key={r.id} data-testid="family-rule">
                  <div className="mkt-parental-list__main">
                    <span>
                      <Badge tone="neutral">{t(`family.kind.${r.subjectKind}`)}</Badge>{" "}
                      {name && <bdi>{name}</bdi>}
                    </span>
                    {r.expiresAt && (
                      <span className="mkt-muted">
                        {t(ended ? "family.rule.ended" : "family.rule.until", {
                          when: formatDate(r.expiresAt),
                        })}
                      </span>
                    )}
                  </div>
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy}
                    onClick={() => void remove(r)}
                  >
                    {t("family.remove")}
                    <span className="mkt-visually-hidden"> {name}</span>
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </section>
    );
  };

  return (
    <section aria-labelledby="family-rules" data-testid="family-rules">
      <h2 id="family-rules">{t("family.rule.title")}</h2>
      <p className="mkt-parental-note">{t("family.rule.intro")}</p>
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} />}
      {data && (
        <>
          {listOf("allow")}
          {listOf("block")}
          <h3>{t("family.rule.add")}</h3>
          <div className="mkt-parental-row">
            <Select
              label={t("family.rule.effect")}
              value={effect}
              onChange={(e) => setEffect(e.target.value as RuleEffect)}
            >
              {EFFECTS.map((e) => (
                <option key={e} value={e}>
                  {t(`family.rule.effect.${e}`)}
                </option>
              ))}
            </Select>
            <SubjectPicker value={picked} onChange={setPicked} />
            <ExpirySelect value={expiry} onChange={setExpiry} />
            <Button disabled={!picked || busy} loading={busy} onClick={() => void add()}>
              {t("family.add")}
            </Button>
          </div>
          {error && (
            <p className="mkt-alert" role="alert">
              {error}
            </p>
          )}
        </>
      )}
    </section>
  );
}
