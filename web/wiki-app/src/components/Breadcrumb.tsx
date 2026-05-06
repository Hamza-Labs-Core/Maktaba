import { Link } from '@tanstack/react-router';
import { ChevronRight } from 'lucide-react';
import { Fragment } from 'react';

export interface Crumb {
  label: string;
  to?: string;
}

export function Breadcrumb({ items }: { items: Crumb[] }) {
  return (
    <nav className="flex flex-wrap items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400 mb-4">
      {items.map((c, i) => (
        <Fragment key={i}>
          {i > 0 && (
            <ChevronRight
              className="h-3 w-3 opacity-50 rtl:rotate-180"
              aria-hidden
            />
          )}
          {c.to ? (
            <Link
              to={c.to}
              className="hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
            >
              {c.label}
            </Link>
          ) : (
            <span className="text-slate-700 dark:text-slate-300">{c.label}</span>
          )}
        </Fragment>
      ))}
    </nav>
  );
}
