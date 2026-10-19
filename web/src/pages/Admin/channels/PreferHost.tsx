// "Prefer host" (Epic 32, story 32.5): which streaming machine this channel
// should encode on, or Automatic. Written at once to tv_cluster.pins[slug]
// through the rules API, keeping every other channel's pin. Shown only when
// the cluster view loads (an admin, on a server that has it).
import { useEffect, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../../lib/i18n";
import { clusterErrText, tvClusterApi } from "../../../lib/tvClusterApi";

/** The one host a pin names ({slug: [host]}, or a bare host name). */
function pinOf(pins: Record<string, unknown>, slug: string): string {
  const v = pins[slug];
  if (Array.isArray(v)) return typeof v[0] === "string" ? v[0] : "";
  return typeof v === "string" ? v : "";
}

export function PreferHost({ slug }: { slug: string }) {
  const { t } = useI18n();
  const toast = useToast();
  const [hosts, setHosts] = useState<string[] | null>(null);
  const [pins, setPins] = useState<Record<string, string[]>>({});
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let alive = true;
    tvClusterApi
      .get()
      .then((v) => {
        if (!alive) return;
        setHosts([...new Set(v.hosts.map((h) => h.name))]);
        setPins(v.settings.pins ?? {});
      })
      .catch(() => alive && setHosts(null));
    return () => {
      alive = false;
    };
  }, []);

  if (!hosts) return null;
  const value = pinOf(pins, slug);
  const names = value && !hosts.includes(value) ? [...hosts, value] : hosts;

  async function choose(host: string) {
    setBusy(true);
    try {
      await tvClusterApi.setPin(pins, slug, host || null);
      setPins((p) => {
        const next = { ...p };
        if (host) next[slug] = [host];
        else delete next[slug];
        return next;
      });
      toast.show({ tone: "success", message: t("tv.cluster.pin.saved") });
    } catch (e) {
      toast.show({
        tone: "error",
        message: t("tv.cluster.pin.failed", { why: clusterErrText(e) }),
      });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Select
      label={t("tv.cluster.pin.label")}
      value={value}
      disabled={busy}
      description={t("tv.cluster.pin.hint")}
      options={[
        { value: "", label: t("tv.cluster.automatic") },
        ...names.map((h) => ({ value: h, label: h })),
      ]}
      onChange={(e) => choose(e.target.value)}
    />
  );
}
