// Signing in a TV with its code. The TV shows a code and a QR of
// /link?code=…; the person signed in here approves it, and the TV is
// signed in as them.
//
// Real contracts (api/internal/handlers/discovery/devicelink.go):
//   GET  /api/auth/device?code=   { code, device_kind, device_label, expires_at }
//   POST /api/auth/device/approve { code }
import { useEffect, useState, type FormEvent } from "react";
import { useSearchParams } from "react-router-dom";
import { Card } from "@ds/components/Card/Card";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { api, ApiError } from "../../lib/api";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";

interface PendingDevice {
  code: string;
  device_kind: string;
  device_label: string;
  expires_at: string;
}

type Step =
  | { kind: "enter"; error?: string }
  | { kind: "checking" }
  | { kind: "confirm"; device: PendingDevice; error?: string }
  | { kind: "approving"; device: PendingDevice }
  | { kind: "done"; device: PendingDevice };

export function LinkDevice() {
  const { t } = useI18n();
  const { user } = useAuth();
  const [params] = useSearchParams();
  const [code, setCode] = useState(params.get("code") ?? "");
  const [step, setStep] = useState<Step>({ kind: "enter" });

  function failure(e: unknown): string {
    if (e instanceof ApiError && (e.problem.status === 404 || e.problem.status === 410))
      return t("link.notFound");
    if (e instanceof ApiError && e.problem.status === 409) return t("link.taken");
    return e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error");
  }

  async function check(typed: string) {
    const c = typed.trim();
    if (!c) return;
    setStep({ kind: "checking" });
    try {
      const device = await api.get<PendingDevice>(`/api/auth/device?code=${encodeURIComponent(c)}`);
      setStep({ kind: "confirm", device });
    } catch (e) {
      setStep({ kind: "enter", error: failure(e) });
    }
  }

  // A QR brings the code along: go straight to what it names.
  useEffect(() => {
    const fromLink = params.get("code");
    if (fromLink) void check(fromLink);
  }, []);

  async function approve(device: PendingDevice) {
    setStep({ kind: "approving", device });
    try {
      await api.post("/api/auth/device/approve", { code: device.code });
      setStep({ kind: "done", device });
    } catch (e) {
      setStep({ kind: "confirm", device, error: failure(e) });
    }
  }

  function submit(e: FormEvent) {
    e.preventDefault();
    void check(code);
  }

  const deviceName = (d: PendingDevice) => d.device_label || t("link.aDevice");

  return (
    <section className="mkt-page mkt-link-device">
      <header className="mkt-page__header">
        <h1>{t("link.title")}</h1>
      </header>
      <Card className="mkt-link-device__card">
        {(step.kind === "enter" || step.kind === "checking") && (
          <form onSubmit={submit} className="mkt-stack">
            <p>{t("link.intro")}</p>
            <Input
              label={t("link.code")}
              value={code}
              onChange={(e) => setCode(e.target.value.toUpperCase())}
              autoComplete="one-time-code"
              autoCapitalize="characters"
              spellCheck={false}
              placeholder="ABCD-1234"
              error={step.kind === "enter" ? step.error : undefined}
            />
            <div>
              <Button type="submit" loading={step.kind === "checking"} disabled={!code.trim()}>
                {t("link.continue")}
              </Button>
            </div>
          </form>
        )}
        {(step.kind === "confirm" || step.kind === "approving") && (
          <div className="mkt-stack">
            <p>
              {t("link.confirm", { device: deviceName(step.device), user: user?.username ?? "" })}
            </p>
            <p className="mkt-muted mkt-mono">{step.device.code}</p>
            {step.kind === "confirm" && step.error && (
              <p role="alert" className="mkt-error">
                {step.error}
              </p>
            )}
            <div className="mkt-row">
              <Button loading={step.kind === "approving"} onClick={() => approve(step.device)}>
                {t("link.approve")}
              </Button>
              <Button variant="ghost" onClick={() => setStep({ kind: "enter" })}>
                {t("common.cancel")}
              </Button>
            </div>
          </div>
        )}
        {step.kind === "done" && (
          <p role="status">{t("link.done", { device: deviceName(step.device) })}</p>
        )}
      </Card>
    </section>
  );
}
