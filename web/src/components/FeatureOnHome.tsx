// Epic 45 Phase 3 (story 45.21) — admins, on a video's page: "Feature on Home"
// makes the lesson Home's hero (PUT /api/home/featured); pressed again it goes
// back to the newest series. Nothing shows for anyone else (the server refuses
// them too).
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useToast } from "@ds/components/Toast/Toast";
import { useAuth } from "../lib/auth";
import { useI18n } from "../lib/i18n";
import { fetchFeaturedPick, setFeaturedPick } from "../lib/browse";
import { errorText } from "../lib/errorText";

export function FeatureOnHome({ videoId }: { videoId: string }) {
  const { t } = useI18n();
  const { user } = useAuth();
  const toast = useToast();
  const admin = !!user?.is_admin;
  const [pick, setPick] = useState<string | null | undefined>(undefined);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!admin) return;
    let cancelled = false;
    fetchFeaturedPick()
      .then((p) => !cancelled && setPick(p))
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [admin, videoId]);

  if (!admin || pick === undefined) return null;
  const featured = pick === videoId;

  async function toggle() {
    setBusy(true);
    try {
      const next = await setFeaturedPick(featured ? null : videoId);
      setPick(next);
      toast.show({
        tone: "success",
        message: t(next === videoId ? "video.featured.on" : "video.featured.off"),
      });
    } catch (e) {
      toast.show({
        tone: "error",
        message: errorText(e, t),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Button variant="ghost" aria-pressed={featured} disabled={busy} onClick={() => void toggle()}>
      {featured ? `★ ${t("video.featured")}` : `☆ ${t("video.feature")}`}
    </Button>
  );
}
