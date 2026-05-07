import { Link } from '@tanstack/react-router';
import type { SearchHit } from '@/types/wiki';
import { highlightTerms, makeSnippet } from '@/lib/markdown';
import { TagChip } from './TagChip';
import { Breadcrumb } from './Breadcrumb';

interface Props {
  query: string;
  hits: SearchHit[];
}

export function SearchResults({ query, hits }: Props) {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);

  return (
    <div>
      <Breadcrumb items={[{ label: 'Home', to: '/' }, { label: 'Search' }]} />
      <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
        Search
      </h1>

      {!query.trim() ? (
        <p className="mt-3 text-slate-500 dark:text-slate-400">
          Type to search across all wiki entries.
        </p>
      ) : (
        <>
          <p className="mt-3 mb-4 text-[13px] text-slate-500 dark:text-slate-400">
            Found {hits.length} {hits.length === 1 ? 'result' : 'results'} for{' '}
            <strong className="text-slate-700 dark:text-slate-200">
              "{query}"
            </strong>
          </p>
          <div className="space-y-2">
            {hits.map((h) => (
              <Link
                key={h.entry.id}
                to="/entry/$id"
                params={{ id: h.entry.id }}
                className="block rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3.5 py-3 hover:border-brand-500 hover:bg-slate-50 dark:hover:bg-slate-900/60 transition-colors"
              >
                <div className="flex items-center gap-2 mb-1">
                  <TagChip type={h.entry.type} />
                  <span
                    className="font-semibold text-slate-900 dark:text-slate-100"
                    dangerouslySetInnerHTML={{
                      __html: highlightTerms(h.entry.title || h.entry.id, tokens),
                    }}
                  />
                  <code className="ml-auto text-[10.5px] text-slate-400 font-mono">
                    {h.entry.id}
                  </code>
                </div>
                <div
                  className="text-[12.5px] text-slate-600 dark:text-slate-300 line-clamp-2"
                  dangerouslySetInnerHTML={{
                    __html: makeSnippet(h.entry.content, tokens),
                  }}
                />
              </Link>
            ))}
            {hits.length === 0 && (
              <p className="text-slate-500 dark:text-slate-400 text-center py-8">
                No matches.
              </p>
            )}
          </div>
        </>
      )}
    </div>
  );
}
