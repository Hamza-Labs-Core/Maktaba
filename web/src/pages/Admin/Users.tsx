// Admin user management (Story 10.1 AC-3 surface).
//
// Real contracts — the server exposes only MUTATION routes; there is no
// `GET /api/users` list or `GET /api/users/{id}`:
//   POST   /api/users                                  { username, password, is_admin }
//   PATCH  /api/users/{id}                             { username?, password?, is_admin? }
//   DELETE /api/users/{id}
//   POST   /api/users/{id}/unlock
//   DELETE /api/users/{id}/sessions/{session_id}
//   DELETE /api/users/{id}/refresh-tokens/{family_id}
//
// Because no listing endpoint exists, rendering a "full roster" table
// would be a false green (see Settings.tsx for this project's deferral
// convention). Instead the table tracks users created/added in this
// browser (persisted to localStorage) and the management actions work
// against any user by ID. The user shape is {id, username, is_admin} —
// no email/role-string/created-date/lock-status is returned by the API.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Badge } from "@ds/components/Badge/Badge";
import { Modal } from "@ds/components/Modal/Modal";
import { Input } from "@ds/components/Input/Input";
import { Checkbox } from "@ds/components/Choice/Checkbox";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";

interface AdminUser {
  id: string;
  username: string;
  is_admin: boolean;
}

const ROSTER_KEY = "mkt:admin:knownUsers";

function readRoster(): AdminUser[] {
  try {
    const raw = localStorage.getItem(ROSTER_KEY);
    if (raw) return JSON.parse(raw) as AdminUser[];
  } catch {
    /* ignored — locked-down or corrupt storage */
  }
  return [];
}

function writeRoster(users: AdminUser[]) {
  try {
    localStorage.setItem(ROSTER_KEY, JSON.stringify(users));
  } catch {
    /* ignored */
  }
}

export function AdminUsers() {
  return (
    <AdminGate>
      <UsersInner />
    </AdminGate>
  );
}

function UsersInner() {
  const { t } = useI18n();
  const toast = useToast();
  const [users, setUsers] = useState<AdminUser[]>(readRoster);
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<AdminUser | null>(null);
  const [deleting, setDeleting] = useState<AdminUser | null>(null);
  const [revoking, setRevoking] = useState<AdminUser | null>(null);

  function persist(next: AdminUser[]) {
    setUsers(next);
    writeRoster(next);
  }

  function upsert(u: AdminUser) {
    const idx = users.findIndex((x) => x.id === u.id);
    persist(idx >= 0 ? users.map((x) => (x.id === u.id ? u : x)) : [...users, u]);
  }

  async function unlock(u: AdminUser) {
    try {
      await api.post(`/api/users/${encodeURIComponent(u.id)}/unlock`);
      toast.show({ tone: "success", message: t("admin.users.unlocked") });
    } catch (e) {
      toast.show({ tone: "error", message: errMessage(e, t) });
    }
  }

  return (
    <section className="mkt-page mkt-admin">
      <header className="mkt-page__header">
        <h1>{t("admin.users.title")}</h1>
        <Button onClick={() => setCreateOpen(true)}>{t("admin.users.add")}</Button>
      </header>

      <p className="mkt-muted mkt-admin__note">{t("admin.users.note")}</p>

      {users.length === 0 ? (
        <EmptyState
          title={t("admin.users.empty.title")}
          description={t("admin.users.empty.desc")}
        />
      ) : (
        <table className="mkt-table" aria-label={t("admin.users.title")}>
          <thead>
            <tr>
              <th>{t("admin.users.col.username")}</th>
              <th>{t("admin.users.col.role")}</th>
              <th>{t("admin.users.col.id")}</th>
              <th>{t("common.actions")}</th>
            </tr>
          </thead>
          <tbody>
            {users.map((u) => (
              <tr key={u.id}>
                <td>{u.username}</td>
                <td>
                  <Badge tone={u.is_admin ? "accent" : "neutral"}>
                    {u.is_admin ? t("admin.users.role.admin") : t("admin.users.role.user")}
                  </Badge>
                </td>
                <td>
                  <span className="mkt-mono mkt-truncate">{u.id}</span>
                </td>
                <td className="mkt-row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(u)}>
                    {t("common.edit")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => unlock(u)}>
                    {t("admin.users.unlock")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setRevoking(u)}>
                    {t("admin.users.sessions")}
                  </Button>
                  <Button size="sm" variant="ghost" onClick={() => setDeleting(u)}>
                    {t("common.delete")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {createOpen && (
        <UserFormModal
          mode="create"
          onClose={() => setCreateOpen(false)}
          onSaved={(u) => {
            upsert(u);
            setCreateOpen(false);
          }}
        />
      )}
      {editing && (
        <UserFormModal
          mode="edit"
          user={editing}
          onClose={() => setEditing(null)}
          onSaved={(u) => {
            upsert(u);
            setEditing(null);
          }}
        />
      )}
      {deleting && (
        <DeleteModal
          user={deleting}
          onClose={() => setDeleting(null)}
          onDeleted={(id) => {
            persist(users.filter((x) => x.id !== id));
            setDeleting(null);
          }}
        />
      )}
      {revoking && <RevokeModal user={revoking} onClose={() => setRevoking(null)} />}
    </section>
  );
}

function errMessage(e: unknown, t: (k: string) => string): string {
  if (e instanceof ApiError) return e.problem.detail || e.problem.title;
  return t("common.error");
}

function UserFormModal({
  mode,
  user,
  onClose,
  onSaved,
}: {
  mode: "create" | "edit";
  user?: AdminUser;
  onClose: () => void;
  onSaved: (u: AdminUser) => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [username, setUsername] = useState(user?.username ?? "");
  const [password, setPassword] = useState("");
  const [isAdmin, setIsAdmin] = useState(user?.is_admin ?? false);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    try {
      if (mode === "create") {
        const created = await api.post<AdminUser>("/api/users", {
          username,
          password,
          is_admin: isAdmin,
        });
        toast.show({ tone: "success", message: t("admin.users.created") });
        onSaved(created);
      } else if (user) {
        const body: Record<string, unknown> = {};
        if (username !== user.username) body.username = username;
        if (password) body.password = password;
        if (isAdmin !== user.is_admin) body.is_admin = isAdmin;
        const updated = await api.patch<AdminUser>(
          `/api/users/${encodeURIComponent(user.id)}`,
          body
        );
        toast.show({ tone: "success", message: t("admin.users.updated") });
        onSaved(updated);
      }
    } catch (e) {
      toast.show({ tone: "error", message: errMessage(e, t) });
    } finally {
      setBusy(false);
    }
  }

  const passwordRequired = mode === "create";
  const valid = username.trim() !== "" && (!passwordRequired || password !== "");

  return (
    <Modal
      open
      onClose={onClose}
      title={mode === "create" ? t("admin.users.create.title") : t("admin.users.edit.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button onClick={submit} loading={busy} disabled={!valid}>
            {t("common.save")}
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
          label={t("admin.users.field.username")}
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          autoComplete="off"
          required
        />
        <Input
          type="password"
          label={
            passwordRequired ? t("admin.users.field.password") : t("admin.users.field.newPassword")
          }
          description={passwordRequired ? undefined : t("common.optional")}
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          autoComplete="new-password"
          required={passwordRequired}
        />
        <Checkbox
          label={t("admin.users.field.admin")}
          checked={isAdmin}
          onChange={(e) => setIsAdmin(e.target.checked)}
        />
      </form>
    </Modal>
  );
}

function DeleteModal({
  user,
  onClose,
  onDeleted,
}: {
  user: AdminUser;
  onClose: () => void;
  onDeleted: (id: string) => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const [busy, setBusy] = useState(false);

  async function confirm() {
    setBusy(true);
    try {
      await api.delete(`/api/users/${encodeURIComponent(user.id)}`);
      toast.show({ tone: "success", message: t("admin.users.deleted") });
      onDeleted(user.id);
    } catch (e) {
      toast.show({ tone: "error", message: errMessage(e, t) });
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      dismissable={false}
      title={t("admin.users.delete.title")}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="destructive" onClick={confirm} loading={busy}>
            {t("common.delete")}
          </Button>
        </>
      }
    >
      <p>{t("admin.users.delete.confirm", { name: user.username })}</p>
    </Modal>
  );
}

function RevokeModal({ user, onClose }: { user: AdminUser; onClose: () => void }) {
  const { t } = useI18n();
  const toast = useToast();
  const [sessionId, setSessionId] = useState("");
  const [familyId, setFamilyId] = useState("");
  const [busy, setBusy] = useState(false);

  async function revoke(path: string, ok: boolean) {
    if (!ok) return;
    setBusy(true);
    try {
      await api.delete(path);
      toast.show({ tone: "success", message: t("admin.users.sessions.revoked") });
    } catch (e) {
      toast.show({ tone: "error", message: errMessage(e, t) });
    } finally {
      setBusy(false);
    }
  }

  const uid = encodeURIComponent(user.id);

  return (
    <Modal open onClose={onClose} title={t("admin.users.sessions.title")}>
      <p className="mkt-muted">{t("admin.users.sessions.desc")}</p>
      <div className="mkt-form">
        <Input
          label={t("admin.users.sessions.sessionId")}
          value={sessionId}
          onChange={(e) => setSessionId(e.target.value)}
        />
        <Button
          variant="destructive"
          size="sm"
          loading={busy}
          disabled={!sessionId.trim()}
          onClick={() =>
            revoke(
              `/api/users/${uid}/sessions/${encodeURIComponent(sessionId)}`,
              !!sessionId.trim()
            )
          }
        >
          {t("admin.users.sessions.revokeSession")}
        </Button>
        <hr />
        <Input
          label={t("admin.users.sessions.familyId")}
          value={familyId}
          onChange={(e) => setFamilyId(e.target.value)}
        />
        <Button
          variant="destructive"
          size="sm"
          loading={busy}
          disabled={!familyId.trim()}
          onClick={() =>
            revoke(
              `/api/users/${uid}/refresh-tokens/${encodeURIComponent(familyId)}`,
              !!familyId.trim()
            )
          }
        >
          {t("admin.users.sessions.revokeFamily")}
        </Button>
      </div>
    </Modal>
  );
}
