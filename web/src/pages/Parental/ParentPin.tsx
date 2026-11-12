// Epic 57 story 57.06 on the web: the parent PIN (Family > Parent PIN).
//
//   GET /api/me/parental-pin   {set}
//   PUT /api/me/parental-pin   {pin, password}   204
//
// An admin's or a guardian's own six digits, typed on a child's screen to open one
// thing for the day or give more time. The home asks for the account password
// again, keeps the PIN hashed, and never sends it back; the page keeps neither.
import { useState, type FormEvent } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../lib/i18n";
import { fetchPinSet, refusalCode, setPin, validPin } from "../../lib/familyApi";
import { usePageData } from "../../lib/usePageData";
import { LoadError } from "../../components/LoadError";
import { BackButton } from "../../components/BackButton";
import { TitleRow } from "../../help/HelpLink";
import { familyError } from "./words";
import "../../styles/parental.css";

export function ParentPinPage() {
  const { t } = useI18n();
  const toast = useToast();
  const { data, err, loading, reload, setData } = usePageData<{ set: boolean }>(
    "parental:pin",
    async () => ({ set: await fetchPinSet() })
  );
  const [pin, setPinText] = useState("");
  const [again, setAgain] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mismatch = again !== "" && again !== pin;
  const ready = validPin(pin) && again === pin && password !== "";

  async function save(e: FormEvent) {
    e.preventDefault();
    if (!ready) return;
    setBusy(true);
    setError(null);
    try {
      await setPin(pin, password);
      setData({ set: true });
      setPinText("");
      setAgain("");
      setPassword("");
      toast.show({ tone: "success", message: t("family.pin.saved") });
    } catch (err2) {
      // a wrong password clears only the password: the digits are not the problem
      if (refusalCode(err2) === "password_wrong") setPassword("");
      setError(familyError(err2, t));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="mkt-page">
      <BackButton fallback="/family" />
      <TitleRow>
        <h1>{t("family.pin")}</h1>
      </TitleRow>
      <p className="mkt-parental-note">{t("family.pin.intro")}</p>
      {loading && <p className="mkt-loading">{t("common.loading")}</p>}
      {err != null && !data && <LoadError error={err} onRetry={reload} backTo="/family" />}
      {data && (
        <form className="mkt-form" onSubmit={(e) => void save(e)} data-testid="family-pin-form">
          <p role="status" data-testid="family-pin-state">
            {t(data.set ? "family.pin.isSet" : "family.pin.notSet")}
          </p>
          <Input
            label={t(data.set ? "family.pin.new" : "family.pin.choose")}
            description={t("family.pin.rule")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            maxLength={6}
            dir="ltr"
            value={pin}
            onChange={(e) => setPinText(e.target.value.replace(/[^0-9]/g, ""))}
          />
          <Input
            label={t("family.pin.again")}
            type="password"
            inputMode="numeric"
            autoComplete="off"
            maxLength={6}
            dir="ltr"
            value={again}
            error={mismatch ? t("family.pin.mismatch") : undefined}
            onChange={(e) => setAgain(e.target.value.replace(/[^0-9]/g, ""))}
          />
          <Input
            label={t("family.pin.password")}
            description={t("family.pin.password.desc")}
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {error && (
            <p className="mkt-alert" role="alert">
              {error}
            </p>
          )}
          <div className="mkt-parental-actions">
            <Button type="submit" disabled={!ready || busy} loading={busy}>
              {t(data.set ? "family.pin.change" : "family.pin.set")}
            </Button>
          </div>
        </form>
      )}
    </section>
  );
}
