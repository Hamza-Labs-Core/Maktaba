// Client-side admin gate for the Epic-16/Story-10 admin surfaces. The
// server is the real authority (every admin route re-checks the
// principal and 403s non-admins); this is a UX guard so a non-admin who
// lands on the route sees a localised "admins only" surface instead of
// a wall of failed requests.
import type { ReactNode } from "react";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";

export function AdminGate({ children }: { children: ReactNode }) {
  const { user } = useAuth();
  const { t } = useI18n();
  if (!user?.is_admin) {
    return (
      <section className="mkt-page">
        <ErrorState
          kind="permission"
          title={t("common.forbidden.title")}
          description={t("common.forbidden.desc")}
        />
      </section>
    );
  }
  return <>{children}</>;
}
