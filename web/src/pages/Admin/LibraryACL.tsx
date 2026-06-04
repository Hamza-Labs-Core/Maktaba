// Admin Library-ACL matrix (web-pages-batch2).
//
// Real contracts:
//   GET /api/admin/library-acl  { users:[{id,username}],
//                                 libraries:[{id,name}],
//                                 grants:[{user_id,library_id,role}] }
//   PUT /api/admin/library-acl  { grants:[{user_id,library_id,role}] }
//
// Rows = users, columns = libraries, each cell a permission-level select
// (none/read/write/admin). Only cells that changed from their loaded
// value are sent on Save; "none" revokes the (user, library) row.
import { useEffect, useMemo, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Select } from "@ds/components/Select/Select";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { ErrorState } from "@ds/components/ErrorState/ErrorState";
import { useToast } from "@ds/components/Toast/Toast";
import { api, ApiError } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { AdminGate } from "../../components/AdminGate";

interface UserRef {
  id: string;
  username: string;
}
interface LibraryRef {
  id: string;
  name: string;
}
interface Grant {
  user_id: string;
  library_id: string;
  role: string;
}
interface Matrix {
  users: UserRef[];
  libraries: LibraryRef[];
  grants: Grant[];
}

const ROLES = ["none", "read", "write", "admin"] as const;

function cellKey(userId: string, libId: string): string {
  return `${userId}|${libId}`;
}

function toRoleMap(grants: Grant[]): Record<string, string> {
  const m: Record<string, string> = {};
  for (const g of grants) m[cellKey(g.user_id, g.library_id)] = g.role;
  return m;
}

export function LibraryACL() {
  return (
    <AdminGate>
      <LibraryACLInner />
    </AdminGate>
  );
}

function LibraryACLInner() {
  const { t } = useI18n();
  const toast = useToast();
  const [matrix, setMatrix] = useState<Matrix | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [original, setOriginal] = useState<Record<string, string>>({});
  const [current, setCurrent] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);

  function load() {
    setError(null);
    api
      .get<Matrix>("/api/admin/library-acl")
      .then((m) => {
        setMatrix(m);
        const roles = toRoleMap(m.grants ?? []);
        setOriginal(roles);
        setCurrent(roles);
      })
      .catch((e) => {
        setError(e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"));
        setMatrix({ users: [], libraries: [], grants: [] });
      });
  }

  useEffect(load, []);

  function roleAt(userId: string, libId: string): string {
    return current[cellKey(userId, libId)] ?? "none";
  }

  function setRole(userId: string, libId: string, role: string) {
    setCurrent((prev) => ({ ...prev, [cellKey(userId, libId)]: role }));
  }

  // Diff current vs original → the batch we send on Save.
  const changes = useMemo<Grant[]>(() => {
    const out: Grant[] = [];
    const keys = new Set([...Object.keys(original), ...Object.keys(current)]);
    for (const k of keys) {
      const before = original[k] ?? "none";
      const after = current[k] ?? "none";
      if (before !== after) {
        const [user_id, library_id] = k.split("|");
        out.push({ user_id, library_id, role: after });
      }
    }
    return out;
  }, [original, current]);

  async function save() {
    if (changes.length === 0) return;
    setSaving(true);
    try {
      const res = await api.put<{ grants: Grant[] }>("/api/admin/library-acl", { grants: changes });
      const roles = toRoleMap(res.grants ?? []);
      setOriginal(roles);
      setCurrent(roles);
      toast.show({ tone: "success", message: t("acl.saved") });
    } catch (e) {
      toast.show({
        tone: "error",
        message: e instanceof ApiError ? e.problem.detail || e.problem.title : t("common.error"),
      });
    } finally {
      setSaving(false);
    }
  }

  return (
    <section className="mkt-page mkt-acl">
      <header className="mkt-page__header">
        <h1>{t("acl.title")}</h1>
        <Button onClick={save} loading={saving} disabled={changes.length === 0}>
          {changes.length > 0 ? t("acl.saveCount", { count: changes.length }) : t("common.save")}
        </Button>
      </header>
      <p className="mkt-muted">{t("acl.desc")}</p>

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
      ) : matrix === null ? (
        <p className="mkt-loading">{t("common.loading")}</p>
      ) : matrix.users.length === 0 || matrix.libraries.length === 0 ? (
        <EmptyState title={t("acl.empty.title")} description={t("acl.empty.desc")} />
      ) : (
        <div className="mkt-acl__scroll">
          <table className="mkt-table mkt-acl__table" aria-label={t("acl.title")}>
            <thead>
              <tr>
                <th>{t("acl.col.user")}</th>
                {matrix.libraries.map((lib) => (
                  <th key={lib.id}>{lib.name}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {matrix.users.map((u) => (
                <tr key={u.id}>
                  <th scope="row">{u.username}</th>
                  {matrix.libraries.map((lib) => (
                    <td key={lib.id}>
                      <Select
                        aria-label={t("acl.cellLabel", { user: u.username, library: lib.name })}
                        value={roleAt(u.id, lib.id)}
                        onChange={(e) => setRole(u.id, lib.id, e.target.value)}
                        options={ROLES.map((r) => ({ value: r, label: t(`acl.role.${r}`) }))}
                      />
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
