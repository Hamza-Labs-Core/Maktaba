// Links into the help (Epic 41). HelpLink is a "Learn more" text link;
// PageHelp is the small "?" next to a page title, its topic found from the
// route (registry PAGE_HELP). Both are plain links: open in a new tab, copy,
// share.
import type { ReactNode } from "react";
import { Link, useInRouterContext, useLocation } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { hrefOf, pageHelp, type HelpRef } from "./registry";
import "../styles/help.css";

export function HelpLink({
  to,
  children,
  className,
  label,
}: {
  to: HelpRef;
  children?: ReactNode;
  className?: string;
  /** The accessible name, when the visible text alone is unclear (it must contain it). */
  label?: string;
}) {
  const { t } = useI18n();
  return (
    <Link
      className={className ? `mkt-helplink ${className}` : "mkt-helplink"}
      to={hrefOf(to)}
      aria-label={label}
    >
      {children ?? t("help.learnMore")}
    </Link>
  );
}

/** A round "?" link to a help entry, with an accessible name. */
export function HelpIcon({ to, label }: { to: HelpRef; label?: string }) {
  const { t } = useI18n();
  const name = label ?? t("help.pageHelp", { topic: t(`help.topic.${to.topic}.title`) });
  return (
    <Link className="mkt-helpicon" to={hrefOf(to)} aria-label={name} title={name}>
      <span aria-hidden="true">?</span>
    </Link>
  );
}

function RoutedPageHelp() {
  const { pathname } = useLocation();
  const ref = pageHelp(pathname);
  return ref ? <HelpIcon to={ref} /> : null;
}

/** The "?" for the current page; nothing outside a router or on a page with no entry. */
export function PageHelp() {
  return useInRouterContext() ? <RoutedPageHelp /> : null;
}

/** A page title with its "?" help link beside it. */
export function TitleRow({ children }: { children: ReactNode }) {
  return (
    <div className="mkt-titlerow">
      {children}
      <PageHelp />
    </div>
  );
}
