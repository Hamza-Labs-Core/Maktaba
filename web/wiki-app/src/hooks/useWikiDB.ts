import { useEffect, useMemo, useState } from 'react';
import type { WikiBundle, WikiDB } from '@/types/wiki';
import { buildBundle, buildIndex, type IndexedEntry } from '@/lib/search-index';

interface State {
  bundle: WikiBundle | null;
  index: IndexedEntry[];
  loading: boolean;
  error: string | null;
}

const FILES = [
  'wiki.json',
  'wiki-epics-07-13.json',
  'wiki-epics-14-24.json',
];

async function fetchJSON<T>(url: string): Promise<T | null> {
  try {
    const r = await fetch(url, { cache: 'force-cache' });
    if (!r.ok) return null;
    return (await r.json()) as T;
  } catch {
    return null;
  }
}

export function useWikiDB(): State {
  const [state, setState] = useState<State>({
    bundle: null,
    index: [],
    loading: true,
    error: null,
  });

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const base = `${import.meta.env.BASE_URL.replace(/\/$/, '')}/db`;
      const results = await Promise.all(
        FILES.map((f) => fetchJSON<WikiDB>(`${base}/${f}`)),
      );
      const primary = results[0];
      if (!primary || !Array.isArray(primary.entries)) {
        if (!cancelled)
          setState({
            bundle: null,
            index: [],
            loading: false,
            error: `Could not load wiki database from ${base}/wiki.json`,
          });
        return;
      }
      const bundle = buildBundle(
        {
          entries: primary.entries,
          meta: {
            version: primary.version || '1.0',
            project: primary.project || 'Maktaba',
            description: primary.description || '',
            counts: primary.counts || {},
            generated_from: primary.generated_from || [],
          },
        },
        results.slice(1).filter((x): x is WikiDB => !!x && Array.isArray(x.entries)),
      );
      const index = buildIndex(bundle.entries);
      if (!cancelled) setState({ bundle, index, loading: false, error: null });
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return useMemo(() => state, [state]);
}
