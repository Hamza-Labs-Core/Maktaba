import { Link } from '@tanstack/react-router';
import { useMemo } from 'react';
import type { WikiBundle, WikiEntry } from '@/types/wiki';
import { renderMarkdown } from '@/lib/markdown';
import {
  TYPE_LABEL,
  endpointIdFor,
  githubUrlFor,
} from '@/lib/labels';
import { TagChip } from './TagChip';
import { fileRole, t } from '@/lib/i18n';
import { Breadcrumb, type Crumb } from './Breadcrumb';
import { RelatedCard } from './RelatedCard';
import { MockupViewer } from './MockupViewer';
import { DiagramViewer } from './DiagramViewer';
import { collectDiagramPaths, collectMockupPaths } from '@/lib/asset-paths';

interface Props {
  bundle: WikiBundle;
  id: string;
}

export function EntryPage({ bundle, id }: Props) {
  const entry = bundle.byId.get(id);

  const html = useMemo(() => {
    if (!entry) return '';
    return renderMarkdown(entry.content || '', (rid) => bundle.byId.has(rid));
  }, [entry, bundle.byId]);

  if (!entry) {
    return (
      <div className="text-center py-12">
        <h1 className="text-2xl font-bold text-slate-900 dark:text-slate-100 mb-2">
          {t('entry.notFound')}
        </h1>
        <p className="text-slate-600 dark:text-slate-400 break-words">
          {t('entry.notFound.body')} <code className="font-mono">{id}</code>
        </p>
        <Link
          to="/"
          className="mt-4 inline-block text-brand-600 hover:underline"
        >
          {t('entry.back')}
        </Link>
      </div>
    );
  }

  // Breadcrumbs
  const crumbs: Crumb[] = [{ label: t('home'), to: '/' }];
  crumbs.push({ label: TYPE_LABEL[entry.type], to: `/type/${entry.type}` });
  if (entry.epic && entry.type !== 'epic') {
    const ep = bundle.byId.get(`epic-${entry.epic}`);
    if (ep) crumbs.push({ label: ep.title, to: `/entry/${ep.id}` });
  }
  crumbs.push({ label: entry.title || entry.id });

  // Subtitle bits
  const subParts: React.ReactNode[] = [];
  if (entry.phase) subParts.push(t('entry.phase', { n: entry.phase }));
  if (entry.engine) subParts.push(entry.engine);
  if (entry.epic && entry.type === 'epic') subParts.push(t('entry.epic', { n: entry.epic }));

  const stories = entry.type === 'epic' ? bundle.epicStories.get(entry.id) || [] : [];
  const plans = entry.type === 'epic' ? bundle.epicPlans.get(entry.id) || [] : [];

  const mockupPaths = collectMockupPaths(entry.files);
  const diagramPaths = collectDiagramPaths(entry.files);

  // Related (filter out plans/stories already shown above for epics)
  const related = (entry.related || [])
    .filter((rid) => bundle.byId.has(rid) && rid !== entry.id)
    .map((rid) => bundle.byId.get(rid)!)
    .filter((r) => {
      if (entry.type !== 'epic') return true;
      return !((r.type === 'story' || r.type === 'plan') && r.epic === entry.epic);
    });

  return (
    <article>
      <Breadcrumb items={crumbs} />

      <h1 className="text-2xl sm:text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100 leading-tight break-words">
        {entry.title || entry.id}
      </h1>

      <div className="mt-2 flex flex-wrap gap-1.5 items-center">
        <TagChip type={entry.type} />
        {entry.metadata?.method && (
          <TagChip method={String(entry.metadata.method)} />
        )}
        {entry.metadata?.path && (
          <span dir="ltr" className="font-mono text-[12px] text-slate-600 dark:text-slate-400 bg-slate-100 dark:bg-slate-800 rounded px-2 py-0.5 break-words max-w-full">
            {String(entry.metadata.path)}
          </span>
        )}
        {(entry.tags || []).map((tag) => (
          <TagChip key={tag}>{tag}</TagChip>
        ))}
      </div>

      {(subParts.length > 0 || entry.id) && (
        <div className="mt-3 text-[12.5px] text-slate-500 dark:text-slate-400 flex flex-wrap items-center gap-2">
          {subParts.map((p, i) => (
            <span key={i} className="inline-flex items-center gap-2">
              {i > 0 && <span className="opacity-50">·</span>}
              {p}
            </span>
          ))}
          {subParts.length > 0 && <span className="opacity-50">·</span>}
          <code dir="ltr" className="font-mono break-words">{entry.id}</code>
        </div>
      )}

      <div
        className="md mt-6"
        // Markdown is rendered & escaped inside renderMarkdown.
        dangerouslySetInnerHTML={{ __html: html }}
      />

      {mockupPaths.length > 0 && (
        <Panel title={mockupPaths.length === 1 ? t('entry.mockup') : t('entry.mockups', { n: mockupPaths.length })}>
          <div className="flex flex-col gap-3">
            {mockupPaths.map((p, i) => (
              <MockupViewer key={p} path={p} defaultOpen={i === 0} />
            ))}
          </div>
        </Panel>
      )}

      {diagramPaths.length > 0 && (
        <Panel title={diagramPaths.length === 1 ? t('entry.diagram') : t('entry.diagrams', { n: diagramPaths.length })}>
          <div className="flex flex-col gap-3">
            {diagramPaths.map((p, i) => (
              <DiagramViewer key={p} path={p} defaultOpen={i === 0} />
            ))}
          </div>
        </Panel>
      )}

      {entry.files && Object.keys(entry.files).length > 0 && (
        <Panel title={t('entry.files')}>
          <div className="flex flex-col gap-1">
            {Object.entries(entry.files).flatMap(([role, val]) => {
              const items = Array.isArray(val) ? val : val ? [val] : [];
              return items
                .filter((p): p is string => !!p)
                .map((path) => (
                  <a
                    key={role + path}
                    href={githubUrlFor(path)}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="flex flex-wrap items-baseline gap-x-3 px-2 py-1.5 rounded hover:bg-slate-100 dark:hover:bg-slate-800/60 text-[12.5px] min-w-0"
                  >
                    <span className="text-slate-500 dark:text-slate-400 font-medium min-w-[70px] shrink-0">
                      {fileRole(role)}
                    </span>
                    <span dir="ltr" className="font-mono text-[12px] text-brand-600 dark:text-brand-300 break-words min-w-0">
                      {path}
                    </span>
                  </a>
                ));
            })}
          </div>
        </Panel>
      )}

      {(entry.api_endpoints || []).length > 0 && (
        <Panel title={t('entry.endpoints')}>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {entry.api_endpoints!.map((ep) => {
              const epId = endpointIdFor(ep);
              const target = epId && bundle.byId.has(epId) ? `/entry/${epId}` : null;
              const [method, ...rest] = ep.split(' ');
              const path = rest.join(' ');
              const inner = (
                <>
                  <TagChip method={method} />
                  <span dir="ltr" className="font-mono text-[12px] text-slate-700 dark:text-slate-300 mt-1.5 truncate text-start">
                    {path}
                  </span>
                </>
              );
              return target ? (
                <Link
                  key={ep}
                  to={target}
                  className="flex flex-col rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2.5 hover:border-brand-500 transition-colors"
                >
                  {inner}
                </Link>
              ) : (
                <div
                  key={ep}
                  className="flex flex-col rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2.5"
                >
                  {inner}
                </div>
              );
            })}
          </div>
        </Panel>
      )}

      {(entry.db_tables || []).length > 0 && (
        <Panel title={t('entry.tables')}>
          <div className="grid grid-cols-1 min-[400px]:grid-cols-2 sm:grid-cols-3 lg:grid-cols-4 gap-2">
            {entry.db_tables!.map((table) => {
              const target = `entity-${table}`;
              return bundle.byId.has(target) ? (
                <Link
                  key={table}
                  to="/entry/$id"
                  params={{ id: target }}
                  className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2 hover:border-brand-500 transition-colors"
                >
                  <div className="font-mono text-[12.5px] text-slate-900 dark:text-slate-100 break-words">
                    {table}
                  </div>
                  <div className="text-[10.5px] text-slate-500 mt-0.5">{t('entry.dbTable')}</div>
                </Link>
              ) : (
                <div
                  key={table}
                  className="rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-3 py-2"
                >
                  <div className="font-mono text-[12.5px]">{table}</div>
                </div>
              );
            })}
          </div>
        </Panel>
      )}

      {entry.type === 'epic' && stories.length > 0 && (
        <Panel title={t('entry.stories', { n: stories.length })}>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {stories.map((s) => (
              <RelatedCard key={s.id} entry={s} />
            ))}
          </div>
        </Panel>
      )}

      {entry.type === 'epic' && plans.length > 0 && (
        <Panel title={t('entry.plans', { n: plans.length })}>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {plans.map((p) => (
              <RelatedCard key={p.id} entry={p} />
            ))}
          </div>
        </Panel>
      )}

      {related.length > 0 && (
        <Panel title={t('entry.related')}>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-2">
            {related.map((r: WikiEntry) => (
              <RelatedCard key={r.id} entry={r} />
            ))}
          </div>
        </Panel>
      )}
    </article>
  );
}

function Panel({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mt-6 rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/40 px-4 py-3.5">
      <div className="text-[10.5px] uppercase tracking-wider rtl:normal-case rtl:tracking-normal rtl:text-[12px] font-semibold text-slate-500 dark:text-slate-400 mb-2.5">
        {title}
      </div>
      {children}
    </section>
  );
}
