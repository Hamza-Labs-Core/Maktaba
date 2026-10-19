// A small, safe Markdown renderer for release notes (Story 28.6).
//
// It builds React elements and never sets HTML, so markup in the source
// (<script>, <img onerror>, …) is shown as text: sanitised by construction.
// Links keep only http(s) and mailto URLs. Covered: ATX headings, paragraphs,
// bullet and numbered lists, fenced code blocks, block quotes, horizontal
// rules, **bold**, *italic* / _italic_, `code`, [links](url) and bare URLs —
// what GitHub release notes use.
import type { ReactNode } from "react";

/** safeHref returns the URL when its scheme is allowed, else null. */
export function safeHref(raw: string): string | null {
  const url = raw.trim();
  if (/^(https?:\/\/|mailto:)/i.test(url)) return url;
  return null;
}

type Block =
  | { kind: "heading"; level: number; text: string }
  | { kind: "para"; text: string }
  | { kind: "list"; ordered: boolean; items: string[] }
  | { kind: "code"; text: string }
  | { kind: "quote"; text: string }
  | { kind: "rule" };

const BULLET = /^\s*[-*+]\s+(.*)$/;
const NUMBERED = /^\s*\d+[.)]\s+(.*)$/;

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (/^\s*$/.test(line)) {
      i++;
      continue;
    }
    if (/^\s*(```|~~~)/.test(line)) {
      const fence = line.trim().slice(0, 3);
      const body: string[] = [];
      i++;
      while (i < lines.length && !lines[i]!.trim().startsWith(fence)) body.push(lines[i++]!);
      i++; // closing fence (or end of input)
      out.push({ kind: "code", text: body.join("\n") });
      continue;
    }
    const h = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (h) {
      out.push({ kind: "heading", level: h[1]!.length, text: h[2]! });
      i++;
      continue;
    }
    if (/^\s{0,3}([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.push({ kind: "rule" });
      i++;
      continue;
    }
    if (/^\s*>/.test(line)) {
      const body: string[] = [];
      while (i < lines.length && /^\s*>/.test(lines[i]!)) {
        body.push(lines[i++]!.replace(/^\s*>\s?/, ""));
      }
      out.push({ kind: "quote", text: body.join(" ") });
      continue;
    }
    const ordered = NUMBERED.test(line);
    if (ordered || BULLET.test(line)) {
      const re = ordered ? NUMBERED : BULLET;
      const items: string[] = [];
      while (i < lines.length) {
        const m = re.exec(lines[i]!);
        if (m) {
          items.push(m[1]!);
          i++;
        } else if (items.length > 0 && /^\s{2,}\S/.test(lines[i]!)) {
          items[items.length - 1] += " " + lines[i++]!.trim(); // continuation line
        } else break;
      }
      out.push({ kind: "list", ordered, items });
      continue;
    }
    const body: string[] = [];
    while (
      i < lines.length &&
      !/^\s*$/.test(lines[i]!) &&
      !/^\s*(```|~~~|#{1,6}\s|>)/.test(lines[i]!) &&
      !BULLET.test(lines[i]!) &&
      !NUMBERED.test(lines[i]!)
    ) {
      body.push(lines[i++]!.trim());
    }
    out.push({ kind: "para", text: body.join(" ") });
  }
  return out;
}

// One inline token at a time, earliest match first.
const INLINE =
  /(`[^`]+`)|(\*\*[^*]+\*\*|__[^_]+__)|(\*[^*\s][^*]*\*|_[^_\s][^_]*_)|(\[[^\]]+\]\([^)\s]+\))|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])/;

export function renderInline(text: string, keyBase = "i"): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let n = 0;
  while (rest.length > 0) {
    const m = INLINE.exec(rest);
    if (!m) {
      out.push(rest);
      break;
    }
    if (m.index > 0) out.push(rest.slice(0, m.index));
    const tok = m[0];
    const key = `${keyBase}-${n++}`;
    if (m[1]) {
      out.push(<code key={key}>{tok.slice(1, -1)}</code>);
    } else if (m[2]) {
      out.push(<strong key={key}>{renderInline(tok.slice(2, -2), key)}</strong>);
    } else if (m[3]) {
      out.push(<em key={key}>{renderInline(tok.slice(1, -1), key)}</em>);
    } else if (m[4]) {
      const lm = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(tok)!;
      const href = safeHref(lm[2]!);
      out.push(
        href ? (
          <a key={key} href={href} target="_blank" rel="noreferrer noopener">
            {renderInline(lm[1]!, key)}
          </a>
        ) : (
          <span key={key}>{renderInline(lm[1]!, key)}</span>
        )
      );
    } else {
      out.push(
        <a key={key} href={tok} target="_blank" rel="noreferrer noopener">
          {tok}
        </a>
      );
    }
    rest = rest.slice(m.index + tok.length);
  }
  return out;
}

/** Markdown renders `source` as safe React elements. */
export function Markdown({ source, className }: { source: string; className?: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className={className} dir="auto">
      {blocks.map((b, i) => {
        const key = `b${i}`;
        switch (b.kind) {
          case "heading": {
            // Release notes sit under the page's own h2: shift them down.
            const level = Math.min(6, b.level + 2);
            const H = `h${level}` as "h3" | "h4" | "h5" | "h6";
            return <H key={key}>{renderInline(b.text, key)}</H>;
          }
          case "list": {
            const L = b.ordered ? "ol" : "ul";
            return (
              <L key={key}>
                {b.items.map((it, j) => (
                  <li key={j}>{renderInline(it, `${key}-${j}`)}</li>
                ))}
              </L>
            );
          }
          case "code":
            return (
              <pre key={key}>
                <code>{b.text}</code>
              </pre>
            );
          case "quote":
            return <blockquote key={key}>{renderInline(b.text, key)}</blockquote>;
          case "rule":
            return <hr key={key} />;
          default:
            return <p key={key}>{renderInline(b.text, key)}</p>;
        }
      })}
    </div>
  );
}
