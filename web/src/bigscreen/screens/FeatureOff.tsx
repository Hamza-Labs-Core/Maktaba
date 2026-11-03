// Big screen's gate for a screen whose feature may be off on this home
// (story 51.11): an admin reads that it is off, with a button to Components;
// a viewer who reached it lands on big screen's Home. A screen already open
// when the feature goes off stays (useOpenFeatureGate).
import type { ReactNode } from "react";
import { Navigate, useLocation, useNavigate } from "react-router-dom";
import { useI18n } from "../../lib/i18n";
import { COMPONENTS_PATH, useOpenFeatureGate, type FeatureId } from "../../lib/capabilities";
import { SERVER_PAGES } from "../../lib/edition";
import { setFullScreen } from "../../lib/bigScreen";
import { bigPath } from "../nav";
import { Empty, Screen } from "../ui";

export function BigFeatureRoute({ id, children }: { id: FeatureId; children: ReactNode }) {
  const { key } = useLocation();
  return (
    <Gate key={key} id={id}>
      {children}
    </Gate>
  );
}

function Gate({ id, children }: { id: FeatureId; children: ReactNode }) {
  const gate = useOpenFeatureGate(id);
  if (gate === "show") return <>{children}</>;
  if (gate === "hide") return <Navigate to={bigPath.home} replace />;
  return <BigFeatureOff id={id} />;
}

export function BigFeatureOff({ id }: { id: FeatureId }) {
  const { t } = useI18n();
  const nav = useNavigate();
  return (
    <Screen testId="big-feature-off">
      <Empty>
        <p data-feature-off={id}>
          {t("capabilities.offAdmin", { feature: t(`components.feature.${id}`) })}
        </p>
        {SERVER_PAGES && (
          <button
            type="button"
            className="bs-btn bs-btn--primary"
            data-bs-focus={`feature-off:${id}`}
            data-bs-autofocus
            onClick={() => {
              void setFullScreen(false);
              nav(COMPONENTS_PATH);
            }}
          >
            {t("capabilities.turnOn")}
          </button>
        )}
      </Empty>
    </Screen>
  );
}
