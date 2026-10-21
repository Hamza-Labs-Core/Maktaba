// A help topic's Markdown as React elements (Epic 41), on the app's safe
// renderer: headings carry their {#anchor} as id with a copy-link button,
// in-app links ("/help/…", "/settings/rules") go through the router.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { useToast } from "@ds/components/Toast/Toast";
import { copyText } from "../lib/clipboard";
import { useI18n } from "../lib/i18n";
import { parseBlocks, renderInline, type InlineOptions } from "../lib/markdown";
import { splitHeading } from "./parse";

// In-app links go through the router; "#anchor" is a section of this topic.
const inline = (topicUrl: string): InlineOptions => ({
  internalLink: (href, children, key) => (
    <Link key={key} to={href.startsWith("#") ? topicUrl + href : href}>
      {children}
    </Link>
  ),
});

function CopyAnchor({ url, label }: { url: string; label: string }) {
  const { t } = useI18n();
  const toast = useToast();
  return (
    <button
      type="button"
      className="mkt-help__copy"
      aria-label={t("help.copyLink", { section: label })}
      title={t("help.copyLink", { section: label })}
      onClick={() => {
        void copyText(url).then((ok) =>
          toast.show({
            id: "help-copy",
            tone: ok ? "success" : "error",
            message: t(ok ? "help.copied" : "help.copyFailed"),
            durationMs: 2500,
          })
        );
      }}
    >
      <span aria-hidden="true">#</span>
    </button>
  );
}

export function HelpMarkdown({
  source,
  topicUrl,
  active,
}: {
  source: string;
  /** The topic's URL; a section's link is this plus "#anchor". */
  topicUrl: string;
  /** The anchor in the address bar, highlighted. */
  active: string;
}) {
  const blocks = parseBlocks(source);
  const INLINE = inline(topicUrl);
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  const out: ReactNode[] = [];
  blocks.forEach((b, i) => {
    const key = `b${i}`;
    switch (b.kind) {
      case "heading": {
        if (b.level === 1) return; // the page renders the title
        const { text, anchor } = splitHeading(b.text);
        const H = (b.level === 2 ? "h2" : b.level === 3 ? "h3" : "h4") as "h2" | "h3" | "h4";
        const cls = anchor && anchor === active ? "mkt-help__h mkt-help__h--target" : "mkt-help__h";
        out.push(
          <H key={key} id={anchor ?? undefined} className={cls} tabIndex={anchor ? -1 : undefined}>
            <span>{renderInline(text, key, INLINE)}</span>
            {anchor && <CopyAnchor url={`${origin}${topicUrl}#${anchor}`} label={text} />}
          </H>
        );
        return;
      }
      case "list": {
        const L = b.ordered ? "ol" : "ul";
        out.push(
          <L key={key}>
            {b.items.map((it, j) => (
              <li key={j}>{renderInline(it, `${key}-${j}`, INLINE)}</li>
            ))}
          </L>
        );
        return;
      }
      case "code":
        out.push(
          <pre key={key}>
            <code>{b.text}</code>
          </pre>
        );
        return;
      case "quote":
        out.push(<blockquote key={key}>{renderInline(b.text, key, INLINE)}</blockquote>);
        return;
      case "rule":
        out.push(<hr key={key} />);
        return;
      default:
        out.push(<p key={key}>{renderInline(b.text, key, INLINE)}</p>);
    }
  });
  return <div className="mkt-help__body">{out}</div>;
}
