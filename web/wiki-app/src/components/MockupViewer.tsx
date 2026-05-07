import { useState } from 'react';
import { ChevronDown, ChevronRight, ExternalLink, Maximize2 } from 'lucide-react';
import { mockupUrl } from '@/lib/asset-paths';
import { githubUrlFor } from '@/lib/labels';

interface Props {
  /** Repo-rooted path, e.g. `web/mockups/admin/login.html`. */
  path: string;
  /** Initial expanded state. Defaults to true for the first viewer on a page. */
  defaultOpen?: boolean;
}

export function MockupViewer({ path, defaultOpen = true }: Props) {
  const [open, setOpen] = useState(defaultOpen);
  const url = mockupUrl(path);
  const filename = path.split('/').slice(-1)[0];

  if (!url) {
    return (
      <div className="text-sm text-slate-500 dark:text-slate-400">
        Cannot resolve mockup path:{' '}
        <code className="font-mono">{path}</code>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 overflow-hidden">
      <div className="flex items-center gap-2 border-b border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/60 px-3 py-2">
        <button
          onClick={() => setOpen((v) => !v)}
          className="inline-flex items-center gap-1.5 text-[12.5px] font-medium text-slate-700 dark:text-slate-200 hover:text-brand-600 dark:hover:text-brand-300"
          aria-expanded={open}
        >
          {open ? (
            <ChevronDown className="h-3.5 w-3.5" />
          ) : (
            <ChevronRight className="h-3.5 w-3.5" />
          )}
          {filename}
        </button>
        <div className="flex-1" />
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-[11.5px] text-slate-600 dark:text-slate-300 hover:text-brand-600 dark:hover:text-brand-300 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800"
          title="Open mockup in new tab"
        >
          <Maximize2 className="h-3 w-3" />
          Open
        </a>
        <a
          href={githubUrlFor(path)}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-[11.5px] text-slate-600 dark:text-slate-300 hover:text-brand-600 dark:hover:text-brand-300 px-2 py-1 rounded hover:bg-slate-100 dark:hover:bg-slate-800"
          title="View source on GitHub"
        >
          <ExternalLink className="h-3 w-3" />
          Source
        </a>
      </div>
      {open && (
        <div className="bg-white dark:bg-slate-950">
          <iframe
            src={url}
            title={filename}
            width="100%"
            height={800}
            loading="lazy"
            className="block w-full border-0"
            sandbox="allow-scripts allow-same-origin allow-forms allow-popups"
          />
        </div>
      )}
    </div>
  );
}
