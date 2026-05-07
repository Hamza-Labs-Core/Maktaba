import { Link } from '@tanstack/react-router';
import type { EntryType, WikiBundle } from '@/types/wiki';
import { TYPE_LABEL_PLURAL } from '@/lib/labels';
import { TagChip } from './TagChip';
import { Breadcrumb } from './Breadcrumb';

export function TypeListPage({
  bundle,
  type,
}: {
  bundle: WikiBundle;
  type: EntryType;
}) {
  const arr = bundle.byType.get(type) || [];

  return (
    <div>
      <Breadcrumb
        items={[{ label: 'Home', to: '/' }, { label: TYPE_LABEL_PLURAL[type] }]}
      />
      <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
        {TYPE_LABEL_PLURAL[type]}
      </h1>
      <p className="mt-2 mb-5 text-[13px] text-slate-500 dark:text-slate-400">
        {arr.length} {arr.length === 1 ? 'entry' : 'entries'}
      </p>

      <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 overflow-hidden">
        {arr.map((e, i) => (
          <Link
            key={e.id}
            to="/entry/$id"
            params={{ id: e.id }}
            className={`flex items-center gap-3 px-3 py-2.5 hover:bg-slate-50 dark:hover:bg-slate-800/40 transition-colors ${i > 0 ? 'border-t border-slate-200 dark:border-slate-800' : ''}`}
          >
            <span className="font-mono text-[11px] text-slate-500 dark:text-slate-400 min-w-[140px] shrink-0 truncate">
              {e.id}
            </span>
            <span className="flex-1 min-w-0 font-medium truncate text-slate-900 dark:text-slate-100">
              {e.title || e.id}
            </span>
            <span className="flex flex-wrap gap-1 shrink-0">
              {e.epic && <TagChip>{e.epic}</TagChip>}
              {e.phase && <TagChip>P{e.phase}</TagChip>}
              {e.metadata?.method && (
                <TagChip method={String(e.metadata.method)} />
              )}
            </span>
          </Link>
        ))}
        {arr.length === 0 && (
          <div className="p-8 text-center text-slate-500 dark:text-slate-400">
            No entries.
          </div>
        )}
      </div>
    </div>
  );
}
