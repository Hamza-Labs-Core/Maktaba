import { useMemo } from 'react';
import type { SearchHit } from '@/types/wiki';
import { search, type IndexedEntry } from '@/lib/search-index';

export function useSearch(
  index: IndexedEntry[],
  query: string,
  limit = 100,
): SearchHit[] {
  return useMemo(() => search(index, query, limit), [index, query, limit]);
}
