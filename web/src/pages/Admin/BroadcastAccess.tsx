// Broadcast access (Epic 50 story 50.26): who may watch this home's published channels. The
// home's owner or an admin picks each channel's access (invite only, or anyone with the link
// once the account is verified), the viewer limits, and the invite links and TV codes, which
// they can withdraw in one click (or one device at a time). Viewers never pay.
//
//   GET    /api/broadcast/access                              channels, limits, verification
//   PUT    /api/broadcast/access/channels/{c}                 { access }
//   PUT    /api/broadcast/access/limits                       { max_sessions_per_ip, daily_minutes }
//   GET    /api/broadcast/access/invites                      the invites and their uses
//   POST   /api/broadcast/access/invites                      { channels, label, max_uses, expires_in_days }
//   DELETE /api/broadcast/access/invites/{id}                 withdraw one
//   GET    /api/broadcast/access/invites/{id}/devices         its devices
//   DELETE /api/broadcast/access/invites/{id}/devices/{p}     remove one device
//
// The home's API refuses anyone but an owner or admin (api/internal/broadcastaccess) and Maktaba
// Cloud keeps the rest of the rules; this page only shows them.
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { errorText } from "../../lib/errorText";
import { usePageData } from "../../lib/usePageData";
import { AdminGate } from "../../components/AdminGate";

const BASE = "/api/broadcast/access";

interface Channel {
  id: string;
  slug: string;
  access: "invite" | "open";
  max_viewers: number;
}

interface AccessView {
  channels: Channel[];
  max_sessions_per_ip: number;
  daily_minutes: number;
  max_viewers: number;
  open_access_enabled: boolean;
  open_in_plan: boolean;
  verification: {
    verified: boolean;
    paid_plan: boolean;
    email_confirmed: boolean;
    days_since_first_publish: number;
  };
}

interface Invite {
  id: string;
  url: string;
  short_code: string;
  label: string;
  channels: string[];
  max_uses: number;
  uses: number | null;
  expires_at: string;
  status: "active" | "expired" | "revoked";
}

interface Device {
  id: string;
  first_use: number;
  last_use: number;
  revoked: boolean;
}

/** The refusals this page words itself: the home's and the cloud's codes. */
const CODES = [
  "cloud_not_linked",
  "cloud_unreachable",
  "broadcast_open_needs_verification",
  "broadcast_open_not_enabled",
  "broadcast_disabled",
  "broadcast_limit",
  "bad_channel",
];

type T = ReturnType<typeof useI18n>["t"];

export function accessErrorText(e: unknown, t: T): string {
  if (e instanceof ApiError) {
    const p = e.problem as { code?: unknown; error?: unknown };
    const code = typeof p.code === "string" ? p.code : typeof p.error === "string" ? p.error : "";
    if (CODES.includes(code)) return t(`broadcastAccess.error.${code}`);
  }
  return errorText(e, t, { admin: true });
}

export function BroadcastAccess() {
  return (
    <AdminGate>
      <BroadcastAccessInner />
    </AdminGate>
  );
}

function BroadcastAccessInner() {
  const { t } = useI18n();
  const access = usePageData<AccessView>("broadcast:access", () => api.get<AccessView>(BASE));
  const v = access.data;
  return (
    <div className="mkt-page">
      <h1>{t("broadcastAccess.title")}</h1>
      <p>{t("broadcastAccess.free")}</p>
      {access.err ? (
        <p role="alert">{accessErrorText(access.err, t)}</p>
      ) : !v ? (
        <p>{t("broadcastAccess.loading")}</p>
      ) : !v.channels?.length ? (
        <p>{t("broadcastAccess.noChannels")}</p>
      ) : (
        <Card>
          <ChannelsAccess view={v} reload={access.reload} />
          <ViewerLimits view={v} reload={access.reload} />
          <Invites channels={v.channels} />
        </Card>
      )}
    </div>
  );
}

function useRun(reload: () => void) {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  async function run(fn: () => Promise<unknown>, done?: string) {
    setBusy(true);
    try {
      await fn();
      if (done) toast.show({ tone: "success", message: done });
      reload();
    } catch (err) {
      toast.show({ tone: "error", message: accessErrorText(err, t) });
    } finally {
      setBusy(false);
    }
  }
  return { busy, run };
}

function ChannelsAccess({ view, reload }: { view: AccessView; reload: () => void }) {
  const { t, n } = useI18n();
  const { busy, run } = useRun(reload);
  const ver = view.verification;
  const openReason = !view.open_in_plan
    ? t("broadcastAccess.openNeedsBroadcaster")
    : !view.open_access_enabled
      ? t("broadcastAccess.openNotYet")
      : !ver.verified
        ? t("broadcastAccess.openNeedsVerification")
        : "";
  return (
    <section aria-labelledby="bc-access">
      <h2 id="bc-access">{t("broadcastAccess.access")}</h2>
      {openReason && view.open_in_plan && <p role="note">{openReason}</p>}
      {view.open_in_plan && !ver.verified && (
        <ul data-testid="verification">
          <li>{(ver.paid_plan ? "✓ " : "✗ ") + t("broadcastAccess.verifyPaid")}</li>
          <li>{(ver.email_confirmed ? "✓ " : "✗ ") + t("broadcastAccess.verifyEmail")}</li>
          <li>
            {(ver.days_since_first_publish >= 7 ? "✓ " : "✗ ") +
              t("broadcastAccess.verifyDays", { days: n(ver.days_since_first_publish) })}
          </li>
        </ul>
      )}
      {view.channels.map((c) => (
        <div className="mkt-row" key={c.id}>
          <Select
            label={c.slug}
            value={c.access}
            disabled={busy}
            options={[
              { value: "invite", label: t("broadcastAccess.invite") },
              {
                value: "open",
                label: t("broadcastAccess.open"),
                disabled: openReason !== "" && c.access !== "open",
              },
            ]}
            onChange={(e) =>
              void run(() =>
                api.put(`${BASE}/channels/${encodeURIComponent(c.id)}`, {
                  access: e.currentTarget.value,
                })
              )
            }
          />
          <Badge tone="neutral">
            {c.max_viewers > 0
              ? t("broadcastAccess.cap", { count: n(c.max_viewers) })
              : t("broadcastAccess.capPublisher", { count: n(view.max_viewers) })}
          </Badge>
        </div>
      ))}
    </section>
  );
}

function ViewerLimits({ view, reload }: { view: AccessView; reload: () => void }) {
  const { t } = useI18n();
  const { busy, run } = useRun(reload);
  const [perIP, setPerIP] = useState(String(view.max_sessions_per_ip));
  const [daily, setDaily] = useState(String(view.daily_minutes));
  return (
    <section aria-labelledby="bc-limits">
      <h2 id="bc-limits">{t("broadcastAccess.limits")}</h2>
      <Input
        label={t("broadcastAccess.perIP")}
        description={t("broadcastAccess.perIPHelp")}
        inputMode="numeric"
        value={perIP}
        onChange={(e) => setPerIP(e.currentTarget.value)}
      />
      <Input
        label={t("broadcastAccess.daily")}
        description={t("broadcastAccess.dailyHelp")}
        inputMode="numeric"
        value={daily}
        onChange={(e) => setDaily(e.currentTarget.value)}
      />
      <Button
        variant="secondary"
        loading={busy}
        onClick={() =>
          void run(
            () =>
              api.put(`${BASE}/limits`, {
                max_sessions_per_ip: Number(perIP),
                daily_minutes: Number(daily),
              }),
            t("broadcastAccess.limitsSaved")
          )
        }
      >
        {t("broadcastAccess.save")}
      </Button>
    </section>
  );
}

function Invites({ channels }: { channels: Channel[] }) {
  const { t, n } = useI18n();
  const list = usePageData<{ invites: Invite[] }>("broadcast:invites", () =>
    api.get<{ invites: Invite[] }>(`${BASE}/invites`)
  );
  const { busy, run } = useRun(list.reload);
  const [label, setLabel] = useState("");
  const [uses, setUses] = useState("10");
  const [days, setDays] = useState("30");
  const [channel, setChannel] = useState("");
  const [created, setCreated] = useState<Invite | null>(null);
  const [devicesOf, setDevicesOf] = useState<string | null>(null);

  async function create() {
    const inv = await api.post<Invite>(`${BASE}/invites`, {
      channels: channel ? [channel] : [],
      label: label.trim(),
      max_uses: Number(uses),
      expires_in_days: Number(days),
    });
    setCreated(inv);
    setLabel("");
  }

  return (
    <section aria-labelledby="bc-invites">
      <h2 id="bc-invites">{t("broadcastAccess.invites")}</h2>
      <Input
        label={t("broadcastAccess.inviteLabel")}
        value={label}
        maxLength={80}
        onChange={(e) => setLabel(e.currentTarget.value)}
      />
      <Select
        label={t("broadcastAccess.inviteChannels")}
        value={channel}
        onChange={(e) => setChannel(e.currentTarget.value)}
        options={[
          { value: "", label: t("broadcastAccess.allChannels") },
          ...channels.map((c) => ({ value: c.id, label: c.slug })),
        ]}
      />
      <Input
        label={t("broadcastAccess.maxUses")}
        inputMode="numeric"
        value={uses}
        onChange={(e) => setUses(e.currentTarget.value)}
      />
      <Input
        label={t("broadcastAccess.days")}
        inputMode="numeric"
        value={days}
        onChange={(e) => setDays(e.currentTarget.value)}
      />
      <Button loading={busy} onClick={() => void run(create)}>
        {t("broadcastAccess.create")}
      </Button>
      {created && (
        <p role="status" data-testid="new-invite">
          {t("broadcastAccess.created", { code: created.short_code })} <code>{created.url}</code>
        </p>
      )}
      {list.err != null && <p role="alert">{accessErrorText(list.err, t)}</p>}
      {list.data && list.data.invites.length === 0 && <p>{t("broadcastAccess.noInvites")}</p>}
      <ul>
        {list.data?.invites.map((inv) => (
          <li key={inv.id} data-testid="invite">
            <strong>{inv.label || t("broadcastAccess.unnamed")}</strong>{" "}
            <Badge tone={inv.status === "active" ? "accent" : "neutral"}>
              {t(`broadcastAccess.status.${inv.status}`)}
            </Badge>
            <p>
              {inv.uses == null
                ? t("broadcastAccess.usesUnknown", { max: n(inv.max_uses) })
                : t("broadcastAccess.uses", { used: n(inv.uses), max: n(inv.max_uses) })}
              {" · "}
              {inv.short_code}
            </p>
            {inv.status === "active" && (
              <div className="mkt-row">
                <Button
                  variant="secondary"
                  disabled={busy}
                  onClick={() => setDevicesOf(devicesOf === inv.id ? null : inv.id)}
                >
                  {t("broadcastAccess.devices")}
                </Button>
                <Button
                  variant="destructive"
                  disabled={busy}
                  onClick={() =>
                    void run(
                      () => api.delete(`${BASE}/invites/${encodeURIComponent(inv.id)}`),
                      t("broadcastAccess.revoked")
                    )
                  }
                >
                  {t("broadcastAccess.revoke")}
                </Button>
              </div>
            )}
            {devicesOf === inv.id && <Devices invite={inv.id} />}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Devices({ invite }: { invite: string }) {
  const { t, formatDate } = useI18n();
  const path = `${BASE}/invites/${encodeURIComponent(invite)}/devices`;
  const list = usePageData<{ devices: Device[] }>(`broadcast:devices:${invite}`, () =>
    api.get<{ devices: Device[] }>(path)
  );
  const { busy, run } = useRun(list.reload);
  if (list.err) return <p role="alert">{accessErrorText(list.err, t)}</p>;
  if (!list.data) return <p>{t("broadcastAccess.loading")}</p>;
  if (list.data.devices.length === 0) return <p>{t("broadcastAccess.noDevices")}</p>;
  return (
    <ul data-testid="devices">
      {list.data.devices.map((dev, i) => (
        <li key={dev.id}>
          {t("broadcastAccess.device", { n: n1(i), last: formatDate(dev.last_use) })}{" "}
          {dev.revoked ? (
            <Badge tone="neutral">{t("broadcastAccess.status.revoked")}</Badge>
          ) : (
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void run(() => api.delete(`${path}/${encodeURIComponent(dev.id)}`))}
            >
              {t("broadcastAccess.removeDevice")}
            </Button>
          )}
        </li>
      ))}
    </ul>
  );
}

const n1 = (i: number) => String(i + 1);
