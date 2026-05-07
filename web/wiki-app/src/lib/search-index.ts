import type { SearchHit, WikiBundle, WikiEntry } from '@/types/wiki';

export interface IndexedEntry {
  entry: WikiEntry;
  blob: string; // lowercased, all searchable text concatenated
}

export function buildIndex(entries: WikiEntry[]): IndexedEntry[] {
  return entries.map((entry) => ({
    entry,
    blob: [
      entry.id,
      entry.title || '',
      entry.content || '',
      (entry.tags || []).join(' '),
      (entry.related || []).join(' '),
      (entry.api_endpoints || []).join(' '),
      (entry.db_tables || []).join(' '),
      entry.epic || '',
    ]
      .join(' ')
      .toLowerCase(),
  }));
}

export function search(
  index: IndexedEntry[],
  query: string,
  limit = 100,
): SearchHit[] {
  const q = query.trim().toLowerCase();
  if (!q) return [];
  const tokens = q.split(/\s+/).filter(Boolean);
  if (!tokens.length) return [];

  const hits: SearchHit[] = [];
  for (const item of index) {
    const titleLow = (item.entry.title || '').toLowerCase();
    const idLow = item.entry.id.toLowerCase();
    let score = 0;
    let allMatch = true;
    for (const tok of tokens) {
      const inTitle = titleLow.includes(tok);
      const inId = idLow.includes(tok);
      const inBlob = item.blob.includes(tok);
      if (!inBlob) {
        allMatch = false;
        break;
      }
      if (inTitle) score += 5;
      if (inId) score += 4;
      score += 1;
    }
    if (allMatch && score > 0) hits.push({ entry: item.entry, score });
  }
  hits.sort((a, b) => b.score - a.score);
  return hits.slice(0, limit);
}

export function buildBundle(
  primary: { entries: WikiEntry[]; meta: WikiBundle['meta'] },
  extras: { entries: WikiEntry[] }[] = [],
): WikiBundle {
  const merged = new Map<string, WikiEntry>();
  const add = (arr: WikiEntry[] | undefined) => {
    if (!Array.isArray(arr)) return;
    for (const e of arr) {
      if (!e || !e.id) continue;
      const cur = merged.get(e.id);
      if (!cur) {
        merged.set(e.id, { ...e });
        continue;
      }
      // Merge: prefer richer content, union arrays, prefer non-null linear.
      if ((e.content?.length || 0) > (cur.content?.length || 0))
        cur.content = e.content;
      cur.related = Array.from(
        new Set([...(cur.related || []), ...(e.related || [])]),
      );
      cur.tags = Array.from(new Set([...(cur.tags || []), ...(e.tags || [])]));
      cur.files = { ...(e.files || {}), ...(cur.files || {}) };
      if (!cur.linear && e.linear) cur.linear = e.linear;
    }
  };
  add(primary.entries);
  for (const x of extras) add(x.entries);

  const entries = Array.from(merged.values());
  const natKey = (s: string) => s.replace(/\d+/g, (n) => n.padStart(8, '0'));
  entries.sort((a, b) => natKey(a.id).localeCompare(natKey(b.id)));

  const byId = new Map<string, WikiEntry>();
  const byType = new Map<WikiEntry['type'], WikiEntry[]>();
  const epicStories = new Map<string, WikiEntry[]>();
  const epicPlans = new Map<string, WikiEntry[]>();

  for (const e of entries) {
    byId.set(e.id, e);
    const arr = byType.get(e.type) || [];
    arr.push(e);
    byType.set(e.type, arr);

    if (e.epic) {
      const epicId = `epic-${e.epic}`;
      if (e.type === 'story') {
        const a = epicStories.get(epicId) || [];
        a.push(e);
        epicStories.set(epicId, a);
      } else if (e.type === 'plan') {
        const a = epicPlans.get(epicId) || [];
        a.push(e);
        epicPlans.set(epicId, a);
      }
    }
  }
  for (const arr of byType.values())
    arr.sort((a, b) => natKey(a.id).localeCompare(natKey(b.id)));
  for (const arr of epicStories.values())
    arr.sort((a, b) => natKey(a.id).localeCompare(natKey(b.id)));
  for (const arr of epicPlans.values())
    arr.sort((a, b) => natKey(a.id).localeCompare(natKey(b.id)));

  return {
    meta: primary.meta,
    entries,
    byId,
    byType,
    epicStories,
    epicPlans,
  };
}
