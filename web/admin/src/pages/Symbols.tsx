// /symbols — the symbol tables Maktaba keeps of each release's dSYMs and of its Android apps'
// R8 mappings (Epic 53 stories 53.14 and 53.16; a mapping's arch is "jvm", its binary the app),
// newest first: a forwarded report's Apple or Android crash reads as functions and lines only
// for what is listed here. They are uploaded with `maktaba-symbols upload`.
import { EmptyState } from "@ds/components/EmptyState/EmptyState";
import { api } from "@cloud-shared/api";
import { useI18n } from "@cloud-shared/i18n";
import { LoadError, Loading, Page, useLoad } from "@cloud-shared/ui";

export interface SymbolTable {
  debug_id: string;
  code_file: string;
  arch: string;
  version?: string;
  uploaded_at: string;
  size: number;
}

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
              <th scope="col">{t("admin.symbols.binary")}</th>
              <th scope="col">{t("admin.symbols.arch")}</th>
              <th scope="col">{t("admin.col.version")}</th>
              <th scope="col">{t("admin.symbols.debugId")}</th>
              <th scope="col">{t("admin.symbols.uploaded")}</th>
              <th scope="col">{t("admin.symbols.size")}</th>
            </tr>
          </thead>
          <tbody>
            {data.data.tables.map((s) => (
              <tr key={s.debug_id}>
                <td dir="ltr">{s.code_file}</td>
                <td className="mkc-mono">
                  {s.arch === "jvm" ? t("admin.symbols.mapping") : s.arch}
                </td>
                <td className="mkc-mono">{s.version || "—"}</td>
                <td className="mkc-mono" dir="ltr">
                  {s.debug_id}
                </td>
                <td>{formatDate(s.uploaded_at)}</td>
                <td>{t("admin.symbols.kb", { count: n(Math.ceil(s.size / 1024)) })}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </Page>
  );
}
