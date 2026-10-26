// Small pieces shared by the admin pages.
import { Pagination } from "@ds/components/Pagination/Pagination";
import { useI18n } from "../../i18n";

export function statusTone(status: string): "success" | "warn" | "error" | "neutral" {
  if (status === "active") return "success";
  if (status === "suspended") return "error";
  if (status === "pending_deletion" || status === "deleting") return "warn";
  return "neutral";
}

export function Pager({
  page,
  total,
  size,
  onChange,
}: {
  page: number;
  total: number;
  size: number;
  onChange: (page: number) => void;
}) {
  const { t } = useI18n();
  return (
    <Pagination
      page={page}
      pageCount={Math.max(1, Math.ceil(total / size))}
      onChange={onChange}
      label={t("pager.label")}
      previousLabel={t("pager.previous")}
      nextLabel={t("pager.next")}
      pageLabel={(p) => t("pager.page", { page: p })}
    />
  );
}
