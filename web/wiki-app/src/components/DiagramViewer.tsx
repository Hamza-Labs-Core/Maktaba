import { useEffect, useMemo, useState } from 'react';
import { ChevronDown, ChevronRight, ExternalLink, Maximize2 } from 'lucide-react';
import { diagramUrl } from '@/lib/asset-paths';
import { githubUrlFor } from '@/lib/labels';

interface Props {
  /** Repo-rooted path, e.g. `specs/diagrams/auth-flow.drawio`. */
  path: string;
  defaultOpen?: boolean;
}

type State =
  | { kind: 'loading' }
  | { kind: 'ready'; xml: string }
  | { kind: 'missing' }
  | { kind: 'error'; message: string };

// Build the viewer.diagrams.net URL with the diagram XML in the fragment.
// The `R` fragment prefix tells drawio that the content is the raw XML.
function buildViewerUrl(xml: string): string {
  const params = new URLSearchParams({
    embed: '1',
    ui: 'min',
    spin: '1',
    proto: 'json',
    saveAndExit: '0',
    noSaveBtn: '1',
    noExitBtn: '1',
  });
  // Drawio supports loading XML directly via the URL fragment with the `R`
  // prefix. Bigger diagrams may exceed the URL limit, in which case we fall
  // back to the embed iframe + postMessage protocol below.
  return `https://viewer.diagrams.net/?${params.toString()}#R${encodeURIComponent(xml)}`;
}

export function DiagramViewer({ path, defaultOpen = true }: Props) {
  const [open, setOpen] = useState(defaultOpen);
  const [state, setState] = useState<State>({ kind: 'loading' });
  const url = diagramUrl(path);
  const filename = path.split('/').slice(-1)[0];

  useEffect(() => {
    let cancelled = false;
    if (!url) {
      setState({ kind: 'error', message: 'Bad diagram path' });
      return;
    }
    setState({ kind: 'loading' });
    fetch(url, { cache: 'force-cache' })
      .then(async (r) => {
        if (cancelled) return;
        if (r.status === 404) {
          setState({ kind: 'missing' });
          return;
        }
        if (!r.ok) {
          setState({ kind: 'error', message: `HTTP ${r.status}` });
          return;
        }
        const text = await r.text();
        if (cancelled) return;
        // Some static hosts fall back to index.html when a file is missing
        // and still return 200 — sniff for HTML and treat that as missing.
        const sniff = text.trimStart().slice(0, 80).toLowerCase();
        if (sniff.startsWith('<!doctype html') || sniff.startsWith('<html')) {
          setState({ kind: 'missing' });
          return;
        }
        setState({ kind: 'ready', xml: text });
      })
      .catch((err) => {
        if (cancelled) return;
        setState({ kind: 'error', message: String(err?.message || err) });
      });
    return () => {
      cancelled = true;
    };
  }, [url]);

  // Only build the embed URL once we have the XML — keeps the iframe URL
  // stable across re-renders.
  const embedSrc = useMemo(() => {
    if (state.kind !== 'ready') return null;
    // Cap fragment-encoded payloads to keep within practical URL limits.
    // Anything larger uses postMessage-based loading instead.
    if (state.xml.length < 60_000) return buildViewerUrl(state.xml);
    return 'https://viewer.diagrams.net/?embed=1&proto=json&spin=1&ui=min';
  }, [state]);

  // For large diagrams, push the XML via postMessage once the iframe is ready.
  useEffect(() => {
    if (state.kind !== 'ready') return;
    if (!embedSrc || embedSrc.includes('#R')) return;
    const onMessage = (event: MessageEvent) => {
      if (typeof event.data !== 'string') return;
      try {
        const msg = JSON.parse(event.data);
        if (msg && msg.event === 'init') {
          (event.source as Window | null)?.postMessage(
            JSON.stringify({ action: 'load', xml: state.xml }),
            '*',
          );
        }
      } catch {
        // Ignore non-JSON messages.
      }
    };
    window.addEventListener('message', onMessage);
    return () => window.removeEventListener('message', onMessage);
  }, [state, embedSrc]);

  return (
    <div className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 overflow-hidden">
      <div className="flex items-center gap-2 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/60 px-3 py-2">
        <button
          onClick={() => setOpen((v) => !v)}
          className="inline-flex items-center gap-1.5 text-[12.5px] font-medium text-slate-700 dark:text-slate-200 hover:text-brand-600 dark:hover:text-brand-300"
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
          {filename}
        </button>
        <div className="flex-1" />
        {url && (
          <a
            href={url}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-[11.5px] text-slate-600 dark:text-slate-300 hover:text-brand-600 dark:hover:text-brand-300 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800"
            title="Open raw .drawio file"
          >
            <Maximize2 className="h-3 w-3" />
            Raw
          </a>
        )}
        <a
          href={githubUrlFor(path)}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-[11.5px] text-slate-600 dark:text-slate-300 hover:text-brand-600 dark:hover:text-brand-300 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800"
          title="View source on GitHub"
        >
          <ExternalLink className="h-3 w-3" />
          Source
        </a>
      </div>
      {open && (
        <div className="bg-white dark:bg-slate-950">
          {state.kind === 'loading' && (
            <div className="px-4 py-10 text-center text-sm text-slate-500 dark:text-slate-400">
              Loading diagram…
            </div>
          )}
          {state.kind === 'missing' && (
            <div className="px-4 py-8 text-sm text-slate-600 dark:text-slate-300">
              <div className="font-medium mb-1">Diagram file not yet committed.</div>
              <div className="text-slate-500 dark:text-slate-400">
                Expected at{' '}
                <code className="font-mono text-[12px] bg-slate-100 dark:bg-slate-800 px-1 py-0.5 rounded">
                  {path}
                </code>
                . Once the .drawio file is added to the repo it will render here automatically.
              </div>
            </div>
          )}
          {state.kind === 'error' && (
            <div className="px-4 py-8 text-sm text-amber-600 dark:text-amber-400">
              Could not load diagram: {state.message}
            </div>
          )}
          {state.kind === 'ready' && embedSrc && (
            <iframe
              src={embedSrc}
              title={filename}
              width="100%"
              height={600}
              loading="lazy"
              className="block w-full border-0"
              sandbox="allow-scripts allow-same-origin allow-popups"
            />
          )}
        </div>
      )}
    </div>
  );
}
