import type { ReactNode } from 'react';
import type { EntryType } from '@/types/wiki';
import {
  METHOD_CHIP_CLASS,
  TYPE_CHIP_CLASS,
  TYPE_LABEL,
} from '@/lib/labels';

interface Props {
  type?: EntryType;
  method?: string;
  children?: ReactNode;
  className?: string;
}

export function TagChip({ type, method, children, className = '' }: Props) {
  let classes =
    'inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium';
  if (type) {
    classes += ' ' + TYPE_CHIP_CLASS[type];
  } else if (method) {
    classes +=
      ' font-mono ' +
      (METHOD_CHIP_CLASS[method] ||
        'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200');
  } else {
    classes +=
      ' bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-300';
  }
  return (
    <span className={`${classes} ${className}`}>
      {children ?? (type ? TYPE_LABEL[type] : method)}
    </span>
  );
}
