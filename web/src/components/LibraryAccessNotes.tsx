// Story 59.03: the notes a screen shows when Maktaba may not write in a
// library's folders: a job that failed on a permission (with a link to the
// library's folder access), and the reason an action is off.
import { Link } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import type { FailureItem } from "../lib/queueApi";

/** The failure kinds that mean Maktaba could not write in a library's folders. */
export const PERMISSION_KINDS = ["nas_readonly", "library_read_only", "library_no_access"] as const;

/** The permission kind of a failed job (its signature is "kind" or "kind:message"), or null. */
export function permissionKind(
  it: Pick<FailureItem, "signature" | "reason">
): (typeof PERMISSION_KINDS)[number] | null {
  for (const k of PERMISSION_KINDS) {
    const sig = it.signature ?? "";
    if (it.reason === k || sig === k || sig.startsWith(`${k}:`)) return k;
  }
  return null;
}

/** The path a permission failure names: the first absolute path in its message, else the item's. */
export function permissionPath(it: Pick<FailureItem, "message" | "path">): string {
  const m = /(?:^|[\s"'(:])(\/[^\s"')]+)/.exec(it.message ?? "");
  return m ? m[1] : (it.path ?? "");
}

/** Where a library's folder access is shown (its admin page's section). */
export const folderAccessHref = (libraryId: string) =>
  `/admin/libraries/${encodeURIComponent(libraryId)}#folders`;

/**
 * "Maktaba couldn't write {path}. See this library's access." for a job that
 * failed on a permission, linked to the library's folder access when it is
 * known; null for any other failure.
 */
export function JobPermissionNote({
  item,
}: {
  item: Pick<FailureItem, "signature" | "reason" | "message" | "path" | "library_id">;
}) {
  const { t } = useI18n();
  if (!permissionKind(item)) return null;
  const path = permissionPath(item);
  const text = t("job.permission_denied", { path: path ? `⁦${path}⁩` : "" });
  return (
    <div className="mkt-q-permission" dir="auto">
      {item.library_id ? <Link to={folderAccessHref(item.library_id)}>{text}</Link> : text}
    </div>
  );
}
