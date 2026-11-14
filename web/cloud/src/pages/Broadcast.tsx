// Broadcast (Epic 50 story 50.26): who may watch each home's channels. Viewers never pay and
// need no account: the publisher picks each channel's access (invite only, or anyone with the
// link once the account is verified), the viewer limits, and the invite links and TV codes it
// hands out, which it can withdraw in one click (or one device at a time).
//
//   GET    /v1/homes                                       the account's homes (owned ones here)
//   GET    /v1/broadcast/homes/{h}/access                  channels, limits, verification
//   PUT    /v1/broadcast/homes/{h}/channels/{c}/access     { access, max_viewers }
//   PUT    /v1/broadcast/homes/{h}/viewer-limits           { max_sessions_per_ip, daily_minutes }
//   GET    /v1/broadcast/homes/{h}/invites                 the invites and their uses
//   POST   /v1/broadcast/homes/{h}/invites                 { channels, label, max_uses, expires_in_days }
//   DELETE /v1/broadcast/homes/{h}/invites/{id}            withdraw one
//   GET    /v1/broadcast/homes/{h}/invites/{id}/devices    its devices
//   DELETE /v1/broadcast/homes/{h}/invites/{id}/devices/{p} withdraw one device
//
// The API enforces every rule (only the home's owner gets past /v1/broadcast/homes/{h}); this
// page only shows them.
import { useState } from "react";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useToast } from "@ds/components/Toast/Toast";
import { api } from "../shared/api";
import { useI18n } from "../shared/i18n";
import { Banner, Loading, LoadError, Page, useErrorMessage, useLoad } from "../shared/ui";

interface Home {
  server_id: string;
  name: string;
  owned: boolean;
}

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

export interface Invite {
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

export function Broadcast() {
  const { t } = useI18n();
  const homes = useLoad(
    () => api.get<{ homes: Home[] }>("/v1/homes").then((r) => r.homes.filter((h) => h.owned)),
    "broadcast-homes"
  );
  return (
    <Page title={t("broadcast.page.title")}>
      <p>{t("broadcast.free")}</p>
      {homes.error ? (
        <LoadError error={homes.error} onRetry={homes.reload} />
      ) : !homes.data ? (
        <Loading />
      ) : homes.data.length === 0 ? (
        <p>{t("broadcast.page.noHomes")}</p>
      ) : (
        homes.data.map((h) => <HomeAccess key={h.server_id} home={h} />)
      )}
    </Page>
  );
}

function HomeAccess({ home }: { home: Home }) {
  const { t } = useI18n();
  const base = `/v1/broadcast/homes/${encodeURIComponent(home.server_id)}`;
  const access = useLoad(
    () => api.get<AccessView>(`${base}/access`),
    `broadcast-access-${home.server_id}`
  );
  const v = access.data;
  if (access.error) return <LoadError error={access.error} onRetry={access.reload} />;
  if (!v) return null;
  return (
    <Card header={<h2>{t("broadcast.page.home", { home: home.name })}</h2>}>
      {v.channels.length === 0 ? (
        <p>{t("broadcast.page.noChannels")}</p>
      ) : (
        <>
          <ChannelsAccess base={base} view={v} reload={access.reload} />
          <ViewerLimits base={base} view={v} reload={access.reload} />
          <Invites base={base} channels={v.channels} />
        </>
      )}
    </Card>
  );
}

function useRun(reload: () => void) {
  const toast = useToast();
  const message = useErrorMessage();
  const [busy, setBusy] = useState(false);
  async function run(fn: () => Promise<unknown>, done?: string) {
    setBusy(true);
    try {
      await fn();
      if (done) toast.show({ tone: "success", message: done });
      reload();
    } catch (err) {
      toast.show({ tone: "error", message: message(err) });
    } finally {
      setBusy(false);
    }
  }
  return { busy, run };
}

function ChannelsAccess({
  base,
  view,
  reload,
}: {
  base: string;
  view: AccessView;
  reload: () => void;
}) {
  const { t, n } = useI18n();
  const { busy, run } = useRun(reload);
  const ver = view.verification;
  const openReason = !view.open_in_plan
    ? t("broadcast.access.openNeedsBroadcaster")
    : !view.open_access_enabled
      ? t("broadcast.access.openNotYet")
      : !ver.verified
        ? t("broadcast.openNeedsVerification")
        : "";
  return (
    <section aria-labelledby={`${base}-access`}>
      <h3 id={`${base}-access`}>{t("broadcast.access.title")}</h3>
      {openReason && view.open_in_plan && <Banner tone="info">{openReason}</Banner>}
      {view.open_in_plan && !ver.verified && (
        <ul className="mkc-features" data-testid="verification">
          <li>{(ver.paid_plan ? "✓ " : "✗ ") + t("broadcast.verify.paid")}</li>
          <li>{(ver.email_confirmed ? "✓ " : "✗ ") + t("broadcast.verify.email")}</li>
          <li>
            {(ver.days_since_first_publish >= 7 ? "✓ " : "✗ ") +
              t("broadcast.verify.days", { days: n(ver.days_since_first_publish) })}
          </li>
        </ul>
      )}
      {view.channels.map((c) => (
        <div className="mkc-row" key={c.id}>
          <Select
            label={c.slug}
            value={c.access}
            disabled={busy}
            options={[
              { value: "invite", label: t("broadcast.access.invite") },
              {
                value: "open",
                label: t("broadcast.access.open"),
                disabled: openReason !== "" && c.access !== "open",
              },
            ]}
            onChange={(e) =>
              void run(() =>
                api.put(`${base}/channels/${c.id}/access`, { access: e.currentTarget.value })
              )
            }
          />
          <Badge tone="neutral">
            {c.max_viewers > 0
              ? t("broadcast.access.cap", { count: n(c.max_viewers) })
              : t("broadcast.access.capPublisher", { count: n(view.max_viewers) })}
          </Badge>
        </div>
      ))}
    </section>
  );
}

function ViewerLimits({
  base,
  view,
  reload,
}: {
  base: string;
  view: AccessView;
  reload: () => void;
}) {
  const { t } = useI18n();
  const { busy, run } = useRun(reload);
  const [perIP, setPerIP] = useState(String(view.max_sessions_per_ip));
  const [daily, setDaily] = useState(String(view.daily_minutes));
  return (
    <section aria-labelledby={`${base}-limits`}>
      <h3 id={`${base}-limits`}>{t("broadcast.limits.title")}</h3>
      <div className="mkc-row">
        <Input
          label={t("broadcast.limits.perIP")}
          description={t("broadcast.limits.perIPHelp")}
          inputMode="numeric"
          value={perIP}
          onChange={(e) => setPerIP(e.currentTarget.value)}
        />
        <Input
          label={t("broadcast.limits.daily")}
          description={t("broadcast.limits.dailyHelp")}
          inputMode="numeric"
          value={daily}
          onChange={(e) => setDaily(e.currentTarget.value)}
        />
      </div>
      <Button
        variant="secondary"
        loading={busy}
        onClick={() =>
          void run(
            () =>
              api.put(`${base}/viewer-limits`, {
                max_sessions_per_ip: Number(perIP),
                daily_minutes: Number(daily),
              }),
            t("broadcast.limits.saved")
          )
        }
      >
        {t("common.save")}
      </Button>
    </section>
  );
}

function Invites({ base, channels }: { base: string; channels: Channel[] }) {
  const { t, n } = useI18n();
  const list = useLoad(() => api.get<{ invites: Invite[] }>(`${base}/invites`), `${base}-invites`);
  const { busy, run } = useRun(list.reload);
  const [label, setLabel] = useState("");
  const [uses, setUses] = useState("10");
  const [days, setDays] = useState("30");
  const [channel, setChannel] = useState("");
  const [created, setCreated] = useState<Invite | null>(null);
  const [devicesOf, setDevicesOf] = useState<string | null>(null);

  async function create() {
    const inv = await api.post<Invite>(`${base}/invites`, {
      channels: channel ? [channel] : [],
      label: label.trim(),
      max_uses: Number(uses),
      expires_in_days: Number(days),
    });
    setCreated(inv);
    setLabel("");
  }

  return (
    <section aria-labelledby={`${base}-invites`}>
      <h3 id={`${base}-invites`}>{t("broadcast.invites.title")}</h3>
      <div className="mkc-row">
        <Input
          label={t("broadcast.invites.label")}
          value={label}
          maxLength={80}
          onChange={(e) => setLabel(e.currentTarget.value)}
        />
        <Select
          label={t("broadcast.invites.channels")}
          value={channel}
          onChange={(e) => setChannel(e.currentTarget.value)}
          options={[
            { value: "", label: t("broadcast.invites.allChannels") },
            ...channels.map((c) => ({ value: c.id, label: c.slug })),
          ]}
        />
        <Input
          label={t("broadcast.invites.maxUses")}
          inputMode="numeric"
          value={uses}
          onChange={(e) => setUses(e.currentTarget.value)}
        />
        <Input
          label={t("broadcast.invites.days")}
          inputMode="numeric"
          value={days}
          onChange={(e) => setDays(e.currentTarget.value)}
        />
      </div>
      <Button loading={busy} onClick={() => void run(create)}>
        {t("broadcast.invite.create")}
      </Button>
      {created && (
        <Banner tone="success">
          <span data-testid="new-invite">
            {t("broadcast.invites.created", { code: created.short_code })}{" "}
            <code>{created.url}</code>
          </span>
        </Banner>
      )}
      {list.data && list.data.invites.length === 0 && <p>{t("broadcast.invites.none")}</p>}
      <ul className="mkc-list">
        {list.data?.invites.map((inv) => (
          <li key={inv.id} data-testid="invite">
            <div className="mkc-row mkc-row--between">
              <strong>{inv.label || t("broadcast.invites.unnamed")}</strong>
              <Badge tone={inv.status === "active" ? "accent" : "neutral"}>
                {t(`broadcast.invites.status.${inv.status}`)}
              </Badge>
            </div>
            <p>
              {inv.uses == null
                ? t("broadcast.invites.usesUnknown", { max: n(inv.max_uses) })
                : t("broadcast.invite.uses", { used: n(inv.uses), max: n(inv.max_uses) })}
              {" · "}
              {inv.short_code}
            </p>
            {inv.status === "active" && (
              <div className="mkc-row">
                <Button
                  variant="secondary"
                  disabled={busy}
                  onClick={() => setDevicesOf(devicesOf === inv.id ? null : inv.id)}
                >
                  {t("broadcast.invites.devices")}
                </Button>
                <Button
                  variant="destructive"
                  disabled={busy}
                  onClick={() =>
                    void run(
                      () => api.delete(`${base}/invites/${inv.id}`),
                      t("broadcast.invites.revoked")
                    )
                  }
                >
                  {t("broadcast.invites.revoke")}
                </Button>
              </div>
            )}
            {devicesOf === inv.id && <Devices base={base} invite={inv.id} />}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Devices({ base, invite }: { base: string; invite: string }) {
  const { t, formatDate } = useI18n();
  const path = `${base}/invites/${invite}/devices`;
  const list = useLoad(() => api.get<{ devices: Device[] }>(path), path);
  const { busy, run } = useRun(list.reload);
  if (list.error) return <LoadError error={list.error} onRetry={list.reload} />;
  if (!list.data) return <Loading />;
  if (list.data.devices.length === 0) return <p>{t("broadcast.devices.none")}</p>;
  return (
    <ul className="mkc-list" data-testid="devices">
      {list.data.devices.map((dev, i) => (
        <li key={dev.id} className="mkc-row mkc-row--between">
          <span>
            {t("broadcast.devices.one", { n: String(i + 1), last: formatDate(dev.last_use) })}
          </span>
          {dev.revoked ? (
            <Badge tone="neutral">{t("broadcast.invites.status.revoked")}</Badge>
          ) : (
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void run(() => api.delete(`${path}/${dev.id}`))}
            >
              {t("broadcast.devices.revoke")}
            </Button>
          )}
        </li>
      ))}
    </ul>
  );
}
