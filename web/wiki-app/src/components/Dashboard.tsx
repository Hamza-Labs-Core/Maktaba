import { Link } from '@tanstack/react-router';
import { CircleDot, ExternalLink, Github } from 'lucide-react';
import { GITHUB_ISSUES, GITHUB_REPO, PHASES, phaseText } from '@/lib/labels';
import { t, type Key } from '@/lib/i18n';
import type { EntryType, WikiBundle } from '@/types/wiki';

interface Props {
  bundle: WikiBundle;
}

const STATS: EntryType[] = ['epic', 'story', 'plan', 'endpoint', 'entity', 'diagram', 'mockup', 'review'];

export function Dashboard({ bundle }: Props) {
  const c = bundle.meta.counts;
  const epics = bundle.byType.get('epic') || [];
  const totalStories = c.story || 0;

  const phaseStoryCounts: Record<number, number> = Object.fromEntries(
    PHASES.map((ph) => [ph, 0]),
  );
  for (const e of epics) {
    const ph = e.phase ?? 0;
    if (phaseStoryCounts[ph] !== undefined) {
      phaseStoryCounts[ph] += Number(e.metadata?.story_count) || 0;
    }
  }

  return (
    <div className="space-y-7">
      <div className="rounded-2xl border border-slate-200 dark:border-slate-800 bg-gradient-to-br from-brand-50 via-white to-slate-50 dark:from-brand-950/40 dark:via-slate-950 dark:to-slate-900 p-5 sm:p-7">
        <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
          {t('app.name')}
        </h1>
        <p className="mt-2 max-w-prose text-slate-600 dark:text-slate-300 break-words">
          {bundle.meta.description}
        </p>
        <div className="mt-4 flex flex-wrap gap-2">
          <QuickLink href={GITHUB_ISSUES} icon={<CircleDot className="h-3 w-3" />}>
            {t('dash.issues')}
          </QuickLink>
          <QuickLink href={GITHUB_REPO} icon={<Github className="h-3 w-3" />}>
            {t('dash.repo')}
          </QuickLink>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {STATS.map((type) => (
          <Link
            key={type}
            to="/type/$type"
            params={{ type }}
            className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3.5 hover:border-brand-500 dark:hover:border-brand-400 hover:-translate-y-px hover:shadow-soft transition-all"
          >
            <div className="text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100 leading-none">
              {c[type] || 0}
            </div>
            <div className="mt-1 text-[11px] uppercase tracking-wide rtl:normal-case rtl:tracking-normal rtl:text-[12px] font-medium text-slate-500 dark:text-slate-400 break-words">
              {t(`dash.stat.${type}` as Key)}
            </div>
          </Link>
        ))}
      </div>

      {/* Phases */}
      <section>
        <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
          {t('dash.phases')}
        </h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {PHASES.map((ph) => {
            const count = phaseStoryCounts[ph];
            const pct = totalStories ? Math.round((count / totalStories) * 100) : 0;
            const lbl = phaseText(ph);
            return (
              <div
                key={ph}
                className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3"
              >
                <div className="flex items-start justify-between gap-2 mb-1">
                  <span className="font-semibold text-slate-900 dark:text-slate-100 text-[13.5px] min-w-0">
                    {lbl.title}
                  </span>
                  <span className="shrink-0 rounded-full bg-slate-100 dark:bg-slate-800 px-2 py-0.5 text-[11px] font-medium text-slate-600 dark:text-slate-300">
                    {t('dash.stories', { n: count })}
                  </span>
                </div>
                <p className="text-[12px] text-slate-500 dark:text-slate-400 leading-relaxed line-clamp-2 min-h-[34px]">
                  {lbl.sub}
                </p>
                <div className="mt-2 h-1.5 bg-slate-100 dark:bg-slate-800 rounded overflow-hidden">
                  <div
                    className="h-full bg-gradient-to-r rtl:bg-gradient-to-l from-brand-500 to-violet-500"
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <div className="mt-2 flex flex-wrap justify-between gap-x-2 text-[11px] text-slate-500 dark:text-slate-400">
                  <span>{lbl.range}</span>
                  <span>{t('dash.pctStories', { n: pct })}</span>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      {/* Epics grid */}
      <section>
        <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
          {t('dash.allEpics')}
        </h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {epics.map((e) => {
            const sc = Number(e.metadata?.story_count) || 0;
            const pc = Number(e.metadata?.plan_count) || 0;
            const desc = (e.content || '').slice(0, 130);
            const truncated = (e.content || '').length > 130;
            return (
              <Link
                key={e.id}
                to="/entry/$id"
                params={{ id: e.id }}
                className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3.5 hover:border-brand-500 dark:hover:border-brand-400 hover:-translate-y-px hover:shadow-soft transition-all min-w-0"
              >
                <div className="flex items-start justify-between gap-2 mb-1">
                  <span className="font-semibold text-slate-900 dark:text-slate-100 text-[13.5px] min-w-0 break-words">
                    {e.title}
                  </span>
                  <span className="shrink-0 rounded-full bg-violet-100 dark:bg-violet-900/40 text-violet-800 dark:text-violet-200 px-2 py-0.5 text-[11px] font-medium">
                    P{e.phase || '?'}
                  </span>
                </div>
                <p className="text-[12px] text-slate-500 dark:text-slate-400 leading-relaxed line-clamp-2 min-h-[34px] break-words">
                  {desc}
                  {truncated ? '…' : ''}
                </p>
                <div className="mt-2 flex flex-wrap gap-x-3 text-[11px] text-slate-500 dark:text-slate-400 min-w-0">
                  <span>{t('dash.stories', { n: sc })}</span>
                  <span>{t('dash.plans', { n: pc })}</span>
                  {e.engine && <span className="truncate max-w-full">{e.engine}</span>}
                </div>
              </Link>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function QuickLink({
  href,
  icon,
  children,
}: {
  href: string;
  icon: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="inline-flex items-center gap-1.5 rounded-full border border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 px-3.5 py-1.5 text-[12.5px] text-slate-600 dark:text-slate-300 hover:border-brand-500 hover:text-brand-700 dark:hover:text-brand-300 hover:bg-brand-50 dark:hover:bg-brand-950/50 transition-colors"
    >
      {icon}
      {children}
      <ExternalLink className="h-2.5 w-2.5 opacity-50" />
    </a>
  );
}
