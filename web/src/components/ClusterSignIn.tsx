// Cluster sign-in prompt (Epic 35, story 35.9 AC6, design §10.5).
//
// On a node whose home offers cluster sign-in, the Login page offers to sign
// in through the home: one click goes node -> home (its own login if needed)
// -> back to this machine with a session for it. A whole-page navigation,
// since the hops are API redirects the router cannot follow.
//
// Shows nothing on the home, on a node without cluster sign-in, or when
// /api/cluster/self cannot be read (an older server).
import { useEffect, useState } from "react";
import { apiUrl } from "../lib/api";
import { clusterApi, type ClusterSelf } from "../lib/clusterApi";
import { bidiIsolate, useI18n } from "../lib/i18n";
import { assignLocation, safeRelativePath, ssoStartUrl } from "../lib/navigation";

export function ClusterSignIn({ returnTo }: { returnTo?: string }) {
  const { t } = useI18n();
  const [self, setSelf] = useState<ClusterSelf | null>(null);

  useEffect(() => {
    let alive = true;
    clusterApi
      .getSelf()
      .then((s) => {
        if (alive) setSelf(s);
      })
      .catch(() => {
        // an older server, or none: no prompt
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!self || self.role !== "node" || !self.sso || !self.home?.web_url) return null;

  const homeName = self.home.name ? bidiIsolate(self.home.name) : null;
  const here = self.name ? bidiIsolate(self.name) : t("clusterSignIn.thisMachine");
  const back = safeRelativePath(returnTo) ?? "/library";

  return (
    <div className="mkt-login__cluster">
      <button
        type="button"
        className="mkt-btn mkt-btn--primary"
        onClick={() => assignLocation(apiUrl(ssoStartUrl(back)))}
      >
        {homeName
          ? t("clusterSignIn.continue", { home: homeName })
          : t("clusterSignIn.continueHome")}
      </button>
      <p className="mkt-login__hint">
        {t("clusterSignIn.where", {
          here,
          home: homeName ?? t("clusterSignIn.theHome"),
        })}
      </p>
      <p className="mkt-login__or">{t("clusterSignIn.or")}</p>
    </div>
  );
}
