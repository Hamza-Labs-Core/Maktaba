// An address the app has no page for (App's catch-all route).
import { Link } from "react-router-dom";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useI18n } from "../lib/i18n";

export function NotFound() {
  const { t } = useI18n();
  return (
    <section className="mkt-page mkt-notfound" data-testid="not-found">
      <h1>{t("notFound.title")}</h1>
      <ErrorState
        kind="not_found"
        title={t("notFound.desc")}
        action={
          <Link to="/" className="mkt-btn mkt-btn--primary">
            {t("error.goHome")}
          </Link>
        }
      />
    </section>
  );
}
