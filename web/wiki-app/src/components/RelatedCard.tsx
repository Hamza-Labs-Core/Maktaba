import { Link } from '@tanstack/react-router';
import type { WikiEntry } from '@/types/wiki';
import { TagChip } from './TagChip';

export function RelatedCard({ entry }: { entry: WikiEntry }) {
  return (
    <Link
      to="/entry/$id"
      params={{ id: entry.id }}
      className="block rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 p-3 hover:border-brand-500 dark:hover:border-brand-400 hover:shadow-soft hover:-translate-y-px transition-all"
    >
      <div className="flex items-center gap-2 mb-1.5">
        <TagChip type={entry.type} />
      </div>
      <div className="font-medium text-sm text-slate-900 dark:text-slate-100 truncate">
        {entry.title || entry.id}
      </div>
      <div className="text-[11px] text-slate-500 dark:text-slate-400 mt-0.5 truncate font-mono">
        {entry.id}
      </div>
    </Link>
  );
}
