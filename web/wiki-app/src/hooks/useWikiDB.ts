import { useEffect, useMemo, useState } from 'react';
import type { WikiBundle, WikiDB } from '@/types/wiki';
import { buildBundle, buildIndex, type IndexedEntry } from '@/lib/search-index';

interface State {
  bundle: WikiBundle | null;
  index: IndexedEntry[];
  loading: boolean;
  error: string | null;
}

// wiki.json names its part files (the stories and plans, by epic range) in `parts`;
// docs/wiki/db/generate_wiki.py writes them all.
const PRIMARY = 'wiki.json';
const PART = /^wiki-epics-\d\d-\d\d\.json$/;

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
      const primary = await fetchJSON<WikiDB>(`${base}/${PRIMARY}`);
      if (!primary || !Array.isArray(primary.entries)) {
        if (!cancelled)
          setState({
            bundle: null,
            index: [],
            loading: false,
            error: `Could not load wiki database from ${base}/${PRIMARY}`,
          });
        return;
      }
      const parts = await Promise.all(
        (primary.parts || [])
          .filter((f) => PART.test(f))
          .map((f) => fetchJSON<WikiDB>(`${base}/${f}`)),
      );
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
        parts.filter((x): x is WikiDB => !!x && Array.isArray(x.entries)),
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
