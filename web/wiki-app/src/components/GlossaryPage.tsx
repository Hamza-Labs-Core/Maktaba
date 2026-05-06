import { Link } from '@tanstack/react-router';
import type { WikiBundle } from '@/types/wiki';
import { TagChip } from './TagChip';
import { Breadcrumb } from './Breadcrumb';

export function GlossaryPage({ bundle }: { bundle: WikiBundle }) {
  const entities = bundle.byType.get('entity') || [];
  const endpoints = bundle.byType.get('endpoint') || [];

  return (
    <div>
      <Breadcrumb items={[{ label: 'Home', to: '/' }, { label: 'Glossary' }]} />
      <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
        Glossary
      </h1>
      <p className="mt-2 mb-6 text-[13px] text-slate-500 dark:text-slate-400">
        Database tables and API endpoints
      </p>

      <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
        DB Tables ({entities.length})
      </h2>
      <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 overflow-hidden mb-7">
        {entities.map((e, i) => (
          <Link
            key={e.id}
            to="/entry/$id"
            params={{ id: e.id }}
            className={`flex items-center gap-3 px-3 py-2 hover:bg-slate-50 dark:hover:bg-slate-800/40 transition-colors ${i > 0 ? 'border-t border-slate-200 dark:border-slate-800' : ''}`}
          >
            <span className="font-mono text-[11px] text-slate-500 dark:text-slate-400 min-w-[64px] shrink-0">
              entity
            </span>
            <span className="font-medium text-slate-900 dark:text-slate-100">
              {e.title}
            </span>
          </Link>
        ))}
      </div>

      <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
        API Endpoints ({endpoints.length})
      </h2>
      <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 overflow-hidden">
        {endpoints.map((e, i) => (
          <Link
            key={e.id}
            to="/entry/$id"
            params={{ id: e.id }}
            className={`flex items-center gap-3 px-3 py-2 hover:bg-slate-50 dark:hover:bg-slate-800/40 transition-colors ${i > 0 ? 'border-t border-slate-200 dark:border-slate-800' : ''}`}
          >
            <span className="font-mono text-[11px] min-w-[64px] shrink-0">
              <TagChip method={String(e.metadata?.method || '')}>
                {String(e.metadata?.method || 'API')}
              </TagChip>
            </span>
            <span className="font-mono text-[12.5px] text-slate-700 dark:text-slate-200 flex-1 min-w-0 truncate">
              {String(e.metadata?.path || e.title)}
            </span>
            {e.metadata?.tag && <TagChip>{String(e.metadata.tag)}</TagChip>}
          </Link>
        ))}
      </div>
    </div>
  );
}
