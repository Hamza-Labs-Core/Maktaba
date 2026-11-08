// /symbols — the symbol tables Maktaba keeps (Epic 53 stories 53.14 and 53.16), newest first: a
// forwarded report's crash reads as functions and lines only for what is listed here. They are
// uploaded with `maktaba-symbols upload`. Three kinds, each keyed its own way:
//   - an Apple release's dSYM ("dsym"): one per binary and architecture, keyed by the binary's UUID;
//   - an Android release's R8 mapping ("r8"): keyed by its app and version (arch "jvm");
//   - an Android app's native library ("elf"): keyed by its GNU build id (code_id, the whole id;
//     debug_id is its first 16 bytes).
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { api } from "@cloud-shared/api";
import { useI18n } from "@cloud-shared/i18n";
import { LoadError, Loading, Page, useLoad } from "@cloud-shared/ui";

export type SymbolKind = "dsym" | "r8" | "elf";

export interface SymbolTable {
  debug_id: string;
  code_file: string;
  arch: string;
  version?: string;
  uploaded_at: string;
  size: number;
  /** absent from an API older than the column, "" for a table whose kind could not be read */
  kind?: SymbolKind | "";
  /** a native library's whole build id */
  code_id?: string;
}

/** The table's kind; an R8 mapping is known by its arch "jvm" even without one. */
export function kindOf(s: SymbolTable): SymbolKind | "" {
  if (s.kind) return s.kind;
  return s.arch === "jvm" ? "r8" : "";
}

/** A 32-digit binary UUID as Apple's tools print it (8-4-4-4-12, upper case). */
export function appleUUID(id: string): string {
  if (!/^[0-9a-f]{32}$/i.test(id)) return id;
  const u = id.toUpperCase();
  return `${u.slice(0, 8)}-${u.slice(8, 12)}-${u.slice(12, 16)}-${u.slice(16, 20)}-${u.slice(20)}`;
}

/** A long build id shortened to its first 12 and last 8 digits; a short one stays whole. */
export function shortBuildId(id: string): string {
  return id.length > 24 ? `${id.slice(0, 12)}…${id.slice(-8)}` : id;
}

const KIND_LABEL = {
  dsym: "admin.symbols.kind.dsym",
  r8: "admin.symbols.kind.r8",
  elf: "admin.symbols.kind.elf",
} as const;

export function AdminSymbols() {
  const { t, n, formatDate } = useI18n();
  const path = "/v1/admin/symbols";
  const data = useLoad(() => api.get<{ tables: SymbolTable[] }>(path), path);
  return (
    <Page title={t("admin.symbols.title")}>
      <p className="mkc-muted">{t("admin.symbols.about")}</p>
      {data.error != null ? (
        <LoadError error={data.error} onRetry={data.reload} />
      ) : !data.data ? (
        <Loading />
      ) : data.data.tables.length === 0 ? (
        <EmptyState kind="first_run" title={t("admin.symbols.none")} />
      ) : (
        <table className="mkc-table" data-testid="symbol-tables">
          <thead>
            <tr>
              <th scope="col">{t("admin.symbols.kind")}</th>
              <th scope="col">{t("admin.symbols.binary")}</th>
              <th scope="col">{t("admin.symbols.arch")}</th>
              <th scope="col">{t("admin.col.version")}</th>
              <th scope="col">{t("admin.symbols.key")}</th>
              <th scope="col">{t("admin.symbols.uploaded")}</th>
              <th scope="col">{t("admin.symbols.size")}</th>
            </tr>
          </thead>
          <tbody>
            {data.data.tables.map((s) => {
              const kind = kindOf(s);
              return (
                <tr key={s.debug_id} data-testid="symbol-table" data-kind={kind || "unknown"}>
                  <td>{kind ? t(KIND_LABEL[kind]) : "—"}</td>
                  <td dir="ltr">{s.code_file}</td>
                  <td className="mkc-mono" dir="ltr">
                    {kind === "r8" ? "—" : s.arch}
                  </td>
                  <td className="mkc-mono" dir="ltr">
                    {s.version || "—"}
                  </td>
                  <td>
                    <TableKey table={s} kind={kind} />
                  </td>
                  <td>{formatDate(s.uploaded_at)}</td>
                  <td>{t("admin.symbols.kb", { count: n(Math.ceil(s.size / 1024)) })}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Page>
  );
}

/** What a table is keyed by, in the form its kind is known by. */
function TableKey({ table: s, kind }: { table: SymbolTable; kind: SymbolKind | "" }) {
  const { t } = useI18n();
  if (kind === "r8") {
    // the key is a hash of these two: show what it stands for
    return (
      <span dir="ltr" title={s.debug_id}>
        {s.code_file} {s.version}
      </span>
    );
  }
  if (kind === "elf" && s.code_id) {
    return (
      <span style={{ whiteSpace: "nowrap" }}>
        <code className="mkc-mono" dir="ltr" title={s.code_id} data-testid="build-id">
          {shortBuildId(s.code_id)}
        </code>{" "}
        <CopyButton value={s.code_id} label={t("admin.symbols.copyBuildId")} />
      </span>
    );
  }
  const shown = kind === "dsym" ? appleUUID(s.debug_id) : s.debug_id;
  return (
    <code className="mkc-mono" dir="ltr" title={s.debug_id}>
      {shown}
    </code>
  );
}

function CopyButton({ value, label }: { value: string; label: string }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        void navigator.clipboard?.writeText(value).then(
          () => setCopied(true),
          () => setCopied(false)
        );
      }}
    >
      <span aria-live="polite">{copied ? t("admin.symbols.copied") : label}</span>
    </Button>
  );
}
