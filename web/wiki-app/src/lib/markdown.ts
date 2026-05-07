// Lightweight Markdown → HTML renderer with cross-reference auto-linking
// for wiki IDs (story-1.1, epic-01-scanner, ep-get-libraries, …).

const escapeHtml = (s: string): string =>
  String(s).replace(/[&<>"']/g, (c) => {
    switch (c) {
      case '&': return '&amp;';
      case '<': return '&lt;';
      case '>': return '&gt;';
      case '"': return '&quot;';
      default: return '&#39;';
    }
  });

const CROSS_REF_RE =
  /\b((?:story|plan|epic|entity|ep|diagram|review|mockup|feature)-[a-z0-9_.\-]+)\b/g;

function applyCrossRefs(html: string, hasId: (id: string) => boolean): string {
  // Replace cross-ref tokens with anchor links, but skip inside existing <a>…</a>.
  const linkRe = /<a [^>]*>[\s\S]*?<\/a>/g;
  const sub = (chunk: string) =>
    chunk.replace(CROSS_REF_RE, (m, id: string) =>
      hasId(id) ? `<a href="#/entry/${id}">${id}</a>` : m,
    );
  let out = '';
  let last = 0;
  let m: RegExpExecArray | null;
  while ((m = linkRe.exec(html)) !== null) {
    out += sub(html.slice(last, m.index));
    out += m[0];
    last = m.index + m[0].length;
  }
  out += sub(html.slice(last));
  return out;
}

function renderInline(text: string, hasId: (id: string) => boolean): string {
  let s = escapeHtml(text);
  s = s.replace(/`([^`]+)`/g, (_m, c) => `<code>${c}</code>`);
  s = s.replace(/\*\*([^*\n]+)\*\*/g, '<strong>$1</strong>');
  s = s.replace(/\b_([^_\n]+)_\b/g, '<em>$1</em>');
  s = s.replace(/(^|[^*])\*([^*\n]+)\*/g, '$1<em>$2</em>');
  // Markdown links [text](url)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (m, t: string, u: string) => {
    const safe =
      u.startsWith('http') ||
      u.startsWith('#') ||
      u.startsWith('/') ||
      u.startsWith('mailto:');
    if (!safe) return m;
    if (u.startsWith('#')) return `<a href="${u}">${t}</a>`;
    return `<a href="${u}" target="_blank" rel="noopener noreferrer">${t}</a>`;
  });
  // Bare URL auto-link
  s = s.replace(
    /(^|[\s(])(https?:\/\/[^\s)]+)/g,
    (_m, pre: string, url: string) =>
      `${pre}<a href="${url}" target="_blank" rel="noopener noreferrer">${url}</a>`,
  );
  return applyCrossRefs(s, hasId);
}

export function renderMarkdown(
  md: string | undefined,
  hasId: (id: string) => boolean,
): string {
  if (!md) return '';
  const lines = md.replace(/\r\n/g, '\n').split('\n');
  const out: string[] = [];
  let i = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Code fence
    if (/^```/.test(line)) {
      const lang = line.replace(/^```/, '').trim();
      const code: string[] = [];
      i++;
      while (i < lines.length && !/^```/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      i++;
      out.push(
        `<pre><code${lang ? ` class="lang-${escapeHtml(lang)}"` : ''}>${escapeHtml(code.join('\n'))}</code></pre>`,
      );
      continue;
    }

    // Heading
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const lvl = h[1].length;
      out.push(`<h${lvl}>${renderInline(h[2], hasId)}</h${lvl}>`);
      i++;
      continue;
    }

    // Horizontal rule
    if (/^\s*(-{3,}|\*{3,}|_{3,})\s*$/.test(line)) {
      out.push('<hr/>');
      i++;
      continue;
    }

    // Blockquote
    if (/^>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ''));
        i++;
      }
      out.push(`<blockquote>${renderMarkdown(buf.join('\n'), hasId)}</blockquote>`);
      continue;
    }

    // Table (very simple GFM support)
    if (
      /\|/.test(line) &&
      i + 1 < lines.length &&
      /^\s*\|?\s*[-:]+/.test(lines[i + 1])
    ) {
      const splitRow = (l: string) =>
        l
          .replace(/^\s*\|/, '')
          .replace(/\|\s*$/, '')
          .split('|')
          .map((c) => c.trim());
      const headers = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && /\|/.test(lines[i]) && lines[i].trim() !== '') {
        rows.push(splitRow(lines[i]));
        i++;
      }
      let html =
        '<table><thead><tr>' +
        headers.map((c) => `<th>${renderInline(c, hasId)}</th>`).join('') +
        '</tr></thead><tbody>';
      for (const r of rows) {
        html +=
          '<tr>' +
          r.map((c) => `<td>${renderInline(c, hasId)}</td>`).join('') +
          '</tr>';
      }
      html += '</tbody></table>';
      out.push(html);
      continue;
    }

    // Lists
    const ulMatch = /^(\s*)[-*+]\s+(.*)$/.exec(line);
    const olMatch = /^(\s*)\d+\.\s+(.*)$/.exec(line);
    if (ulMatch || olMatch) {
      const ordered = !!olMatch;
      const tag = ordered ? 'ol' : 'ul';
      const items: string[] = [];
      while (i < lines.length) {
        const m1 = ordered
          ? /^(\s*)\d+\.\s+(.*)$/.exec(lines[i])
          : /^(\s*)[-*+]\s+(.*)$/.exec(lines[i]);
        if (!m1) break;
        const itemLines = [m1[2]];
        i++;
        while (i < lines.length && /^\s{2,}\S/.test(lines[i])) {
          itemLines.push(lines[i].replace(/^\s{2,}/, ''));
          i++;
        }
        items.push(itemLines.join('\n'));
      }
      let html = `<${tag}>`;
      for (const it of items) {
        html += it.includes('\n')
          ? `<li>${renderMarkdown(it, hasId)}</li>`
          : `<li>${renderInline(it, hasId)}</li>`;
      }
      html += `</${tag}>`;
      out.push(html);
      continue;
    }

    if (line.trim() === '') {
      i++;
      continue;
    }

    // Paragraph
    const buf: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== '' &&
      !/^(#|>|```|\s*([-*+]|\d+\.)\s)/.test(lines[i])
    ) {
      buf.push(lines[i]);
      i++;
    }
    out.push(`<p>${renderInline(buf.join(' '), hasId)}</p>`);
  }
  return out.join('\n');
}

export function highlightTerms(s: string, tokens: string[]): string {
  let h = escapeHtml(s);
  for (const t of tokens) {
    if (!t) continue;
    const re = new RegExp(
      `(${t.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})`,
      'gi',
    );
    h = h.replace(re, '<mark>$1</mark>');
  }
  return h;
}

export function makeSnippet(
  text: string | undefined,
  tokens: string[],
): string {
  if (!text) return '';
  const lower = text.toLowerCase();
  let pos = -1;
  for (const t of tokens) {
    const i = lower.indexOf(t);
    if (i >= 0 && (pos < 0 || i < pos)) pos = i;
  }
  if (pos < 0) return escapeHtml(text.slice(0, 200));
  const start = Math.max(0, pos - 60);
  const end = Math.min(text.length, pos + 140);
  const slice =
    (start > 0 ? '…' : '') + text.slice(start, end) + (end < text.length ? '…' : '');
  return highlightTerms(slice, tokens);
}
