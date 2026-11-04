// A route whose feature may be off on this home (story 51.11): on, the page;
// off, an admin reads the note with a link to Components, and a viewer who
// followed a link there (a shared /live/7) lands on Home with a toast.
// A page already open when the feature goes off stays (useOpenFeatureGate);
// its next call answers 409 component_off.
import { useEffect, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useToast } from "@ds/components/Toast/Toast";
import { FeatureOffNote, useOpenFeatureGate, type FeatureId } from "../lib/capabilities";
import { useI18n } from "../lib/i18n";
import { featureUnavailableText } from "../lib/featureWords";

export function FeatureRoute({
  id,
  home = "/",
  children,
}: {
  id: FeatureId;
  /** where a viewer goes instead */
  home?: string;
  children: ReactNode;
}) {
  const { key } = useLocation();
  // a new navigation decides again
  return (
    <Gate key={key} id={id} home={home}>
      {children}
    </Gate>
  );
}

function Gate({ id, home, children }: { id: FeatureId; home: string; children: ReactNode }) {
  const gate = useOpenFeatureGate(id);
  if (gate === "show") return <>{children}</>;
  if (gate === "explain") {
    return (
      <section className="mkt-page">
        <FeatureOffNote id={id} />
      </section>
    );
  }
  return <Unavailable id={id} home={home} />;
}

function Unavailable({ id, home }: { id: FeatureId; home: string }) {
  const { t } = useI18n();
  const toast = useToast();
  useEffect(() => {
    toast.show({
      id: `feature-unavailable-${id}`,
      tone: "info",
      message: featureUnavailableText(id, t),
    });
  }, [id, t, toast]);
  return <Navigate to={home} replace />;
}
