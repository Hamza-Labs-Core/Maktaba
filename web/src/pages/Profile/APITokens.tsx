// Personal access tokens self-service (web-pages-batch2).
//
// Real contracts:
//   GET    /api/me/tokens        { items: [tokenView] }   (never the secret)
//   POST   /api/me/tokens        { name, scopes?, expires_in_days? }
//                                  → 201 { token, pat }    (raw token ONCE)
//   DELETE /api/me/tokens/{id}   (soft-revoke; 204)
//
// The raw token is shown exactly once after creation with a copy button;
// thereafter only the prefix is visible. Revoked tokens stay in the list
// (greyed) so an audit trail remains.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Modal } from "@ds/components/Modal/Modal";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";

interface TokenView {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
  revoked_at: string | null;
}

// Illustrative scope vocabulary. Empty selection ⇒ inherit the owner's
// full permissions (the server's documented default).
const SCOPES = ["read", "write", "admin"] as const;
const EXPIRY_DAYS = [0, 30, 90, 365] as const;

export function APITokens() {
  const { t, formatDate } = useI18n();
  const toast = useToast();
  const [tokens, setTokens] = useState<TokenView[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [createOpen, setCreateOpen] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [revoking, setRevoking] = useState<TokenView | null>(null);

  function load() {
    setError(null);
    api
      .get<{ items: TokenView[] }>("/api/me/tokens")
      .then((r) => setTokens(r.items ?? []))
      .catch((e) => {
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
        setTokens([]);
      });
  }

  useEffect(load, []);

  async function revoke(tok: TokenView) {
    try {
      await api.delete(`/api/me/tokens/${encodeURIComponent(tok.id)}`);
      toast.show({ tone: "success", message: t("tokens.revoked") });
      setRevoking(null);
      load();
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    }
  }

  return (
    <section className="mkt-page mkt-tokens">
      <header className="mkt-page__header">
        <h1>{t("tokens.title")}</h1>
        <Button onClick={() => setCreateOpen(true)}>{t("tokens.create")}</Button>
      </header>
      <p className="mkt-muted">{t("tokens.desc")}</p>

      {error ? (
        <ErrorState
          kind="server"
          title={t("common.error")}
          description={error}
          action={
            <Button variant="secondary" onClick={load}>
              {t("common.retry")}
            </Button>
          }
        />
      ) : tokens === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : tokens.length === 0 ? (
        <EmptyState title={t("tokens.empty.title")} description={t("tokens.empty.desc")} />
      ) : (
        <table className="mkt-table" aria-label={t("tokens.title")}>
          <thead>
            <tr>
              <th>{t("tokens.col.name")}</th>
              <th>{t("tokens.col.prefix")}</th>
              <th>{t("tokens.col.scopes")}</th>
              <th>{t("tokens.col.lastUsed")}</th>
              <th>{t("tokens.col.expires")}</th>
              <th>{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {tokens.map((tok) => {
              const revoked = tok.revoked_at !== null;
              return (
                <tr key={tok.id} className={revoked ? "mkt-row--muted" : undefined}>
                  <td>
                    {tok.name}
                    {revoked && (
                      <>
                        {" "}
                        <Badge tone="neutral">{t("tokens.revokedBadge")}</Badge>
                      </>
                    )}
                  </td>
                  <td className="mkt-mono">{tok.prefix}…</td>
                  <td>
                    {tok.scopes.length === 0 ? (
                      <span className="mkt-muted">{t("tokens.scopes.full")}</span>
                    ) : (
                      tok.scopes.join(", ")
                    )}
                  </td>
                  <td>{tok.last_used_at ? formatDate(tok.last_used_at) : t("common.never")}</td>
                  <td>{tok.expires_at ? formatDate(tok.expires_at) : t("tokens.noExpiry")}</td>
                  <td className="mkt-row-actions">
                    {!revoked && (
                      <Button size="sm" variant="destructive" onClick={() => setRevoking(tok)}>
                        {t("tokens.revoke")}
                      </Button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}

      {createOpen && (
        <CreateTokenModal
          onClose={() => setCreateOpen(false)}
          onCreated={(raw) => {
            setCreateOpen(false);
            setCreated(raw);
            load();
          }}
        />
      )}

      {created && <RevealTokenModal token={created} onClose={() => setCreated(null)} />}

      {revoking && (
        <Modal
          open
          onClose={() => setRevoking(null)}
          dismissable={false}
          title={t("tokens.revoke.title")}
          footer={
            <>
              <Button variant="ghost" onClick={() => setRevoking(null)}>
                {t("common.cancel")}
              </Button>
              <Button variant="destructive" onClick={() => revoke(revoking)}>
                {t("tokens.revoke")}
              </Button>
            </>
          }
        >
          <p>{t("tokens.revoke.confirm", { name: revoking.name })}</p>
        </Modal>
      )}
    </section>
  );
}

function errMessage(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) return e.problem.detail || e.problem.title;
  return t("common.error");
}

function CreateTokenModal({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: (rawToken: string) => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [name, setName] = useState("");
  const [scopes, setScopes] = useState<string[]>([]);
  const [expiresInDays, setExpiresInDays] = useState(0);
  const [busy, setBusy] = useState(false);

  function toggleScope(scope: string, on: boolean) {
    setScopes((prev) => (on ? [...prev, scope] : prev.filter((s) => s !== scope)));
  }

  async function submit() {
    setBusy(true);
    try {
      const res = await api.post<{ token: string }>("/api/me/tokens", {
        name,
        scopes,
        expires_in_days: expiresInDays,
      });
      toast.show({ tone: "success", message: t("tokens.created") });
      onCreated(res.token);
    } catch (e) {
      toast.show({ tone: "error", message: errMessage(e, t) });
    } finally {
      setBusy(false);
    }
  }

  const valid = name.trim() !== "";

  return (
    <Modal
      open
      onClose={onClose}
      title={t("tokens.create.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy} disabled={!valid}>
            {t("tokens.create")}
          </Button>
        </>
      }
    >
      <form
        className="mkt-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (valid) void submit();
        }}
      >
        <Input
          label={t("tokens.field.name")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          autoComplete="off"
          required
        />
        <fieldset className="mkt-fieldset">
          <legend className="mkt-field__label">{t("tokens.field.scopes")}</legend>
          <p className="mkt-muted">{t("tokens.field.scopesHint")}</p>
          {SCOPES.map((scope) => (
            <Checkbox
              key={scope}
              label={t(`tokens.scope.${scope}`)}
              checked={scopes.includes(scope)}
              onChange={(e) => toggleScope(scope, e.target.checked)}
            />
          ))}
        </fieldset>
        <Select
          label={t("tokens.field.expiry")}
          value={String(expiresInDays)}
          onChange={(e) => setExpiresInDays(Number(e.target.value))}
          options={EXPIRY_DAYS.map((d) => ({
            value: String(d),
            label: d === 0 ? t("tokens.noExpiry") : t("tokens.expiry.days", { count: d }),
          }))}
        />
      </form>
    </Modal>
  );
}

function RevealTokenModal({ token, onClose }: { token: string; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(token);
      setCopied(true);
      toast.show({ tone: "success", message: t("tokens.copied") });
    } catch {
      toast.show({ tone: "error", message: t("tokens.copyFailed") });
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      dismissable={false}
      title={t("tokens.reveal.title")}
      footer={<Button onClick={onClose}>{t("common.close")}</Button>}
    >
      <p className="mkt-alert" role="alert">
        {t("tokens.reveal.warning")}
      </p>
      <div className="mkt-token-reveal">
        <code className="mkt-token-reveal__value mkt-mono">{token}</code>
        <Button size="sm" variant="secondary" onClick={copy}>
          {copied ? t("tokens.copied") : t("tokens.copy")}
        </Button>
      </div>
    </Modal>
  );
}
