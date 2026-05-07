import { Link } from '@tanstack/react-router';
import { ExternalLink, Github, Sparkles, Zap } from 'lucide-react';
import {
  GITHUB_REPO,
  LINEAR_PROJECT,
  PENPOT_LINK,
  PHASE_INFO,
} from '@/lib/labels';
import type { WikiBundle } from '@/types/wiki';

interface Props {
  bundle: WikiBundle;
}

export function Dashboard({ bundle }: Props) {
  const c = bundle.meta.counts;
  const epics = bundle.byType.get('epic') || [];
  const totalStories = c.story || 0;

  const phaseStoryCounts: Record<number, number> = { 1: 0, 2: 0, 3: 0, 4: 0 };
  for (const e of epics) {
    const ph = e.phase ?? 0;
    if (phaseStoryCounts[ph] !== undefined) {
      phaseStoryCounts[ph] += Number(e.metadata?.story_count) || 0;
    }
  }

  const stats: { to: string; params?: Record<string, string>; n: number; label: string }[] = [
    { to: '/type/$type', params: { type: 'epic' }, n: c.epic || 0, label: 'Epics' },
    { to: '/type/$type', params: { type: 'story' }, n: c.story || 0, label: 'Stories' },
    { to: '/type/$type', params: { type: 'plan' }, n: c.plan || 0, label: 'Plans' },
    { to: '/type/$type', params: { type: 'endpoint' }, n: c.endpoint || 0, label: 'API endpoints' },
    { to: '/type/$type', params: { type: 'entity' }, n: c.entity || 0, label: 'DB tables' },
    { to: '/type/$type', params: { type: 'diagram' }, n: c.diagram || 0, label: 'Diagrams' },
    { to: '/type/$type', params: { type: 'mockup' }, n: c.mockup || 0, label: 'Mockups' },
    { to: '/type/$type', params: { type: 'review' }, n: c.review || 0, label: 'Reviews' },
  ];

  return (
    <div className="space-y-7">
      <div className="rounded-2xl border border-slate-200 dark:border-slate-800 bg-gradient-to-br from-brand-50 via-white to-slate-50 dark:from-brand-950/40 dark:via-slate-950 dark:to-slate-900 p-7">
        <h1 className="text-3xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
          Maktaba Wiki
        </h1>
        <p className="mt-2 max-w-prose text-slate-600 dark:text-slate-300">
          {bundle.meta.description}
        </p>
        <div className="mt-4 flex flex-wrap gap-2">
          <QuickLink href={LINEAR_PROJECT} icon={<Zap className="h-3 w-3" />}>
            Linear board
          </QuickLink>
          <QuickLink href={GITHUB_REPO} icon={<Github className="h-3 w-3" />}>
            GitHub repo
          </QuickLink>
          <QuickLink href={PENPOT_LINK} icon={<Sparkles className="h-3 w-3" />}>
            Penpot designs
          </QuickLink>
        </div>
      </div>

      {/* Stats */}
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        {stats.map((s, i) => (
          <Link
            key={i}
            to={s.to}
            params={s.params}
            className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3.5 hover:border-brand-500 dark:hover:border-brand-400 hover:-translate-y-px hover:shadow-soft transition-all"
          >
            <div className="text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100 leading-none">
              {s.n}
            </div>
            <div className="mt-1 text-[11px] uppercase tracking-wide font-medium text-slate-500 dark:text-slate-400">
              {s.label}
            </div>
          </Link>
        ))}
      </div>

      {/* Phases */}
      <section>
        <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
          Phases
        </h2>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
          {[1, 2, 3, 4].map((ph) => {
            const count = phaseStoryCounts[ph];
            const pct = totalStories ? Math.round((count / totalStories) * 100) : 0;
            const lbl = PHASE_INFO[ph];
            return (
              <div
                key={ph}
                className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3"
              >
                <div className="flex items-center justify-between mb-1">
                  <span className="font-semibold text-slate-900 dark:text-slate-100 text-[13.5px]">
                    {lbl.title}
                  </span>
                  <span className="rounded-full bg-slate-100 dark:bg-slate-800 px-2 py-0.5 text-[11px] font-medium text-slate-600 dark:text-slate-300">
                    {count} stories
                  </span>
                </div>
                <p className="text-[12px] text-slate-500 dark:text-slate-400 leading-relaxed line-clamp-2 min-h-[34px]">
                  {lbl.sub}
                </p>
                <div className="mt-2 h-1.5 bg-slate-100 dark:bg-slate-800 rounded overflow-hidden">
                  <div
                    className="h-full bg-gradient-to-r from-brand-500 to-violet-500"
                    style={{ width: `${pct}%` }}
                  />
                </div>
                <div className="mt-2 flex justify-between text-[11px] text-slate-500 dark:text-slate-400">
                  <span>{lbl.range}</span>
                  <span>{pct}% of stories</span>
                </div>
              </div>
            );
          })}
        </div>
      </section>

      {/* Epics grid */}
      <section>
        <h2 className="text-base font-bold text-slate-900 dark:text-slate-100 mb-3">
          All epics
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
                className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 px-4 py-3.5 hover:border-brand-500 dark:hover:border-brand-400 hover:-translate-y-px hover:shadow-soft transition-all"
              >
                <div className="flex items-center justify-between mb-1">
                  <span className="font-semibold text-slate-900 dark:text-slate-100 text-[13.5px]">
                    {e.title}
                  </span>
                  <span className="rounded-full bg-violet-100 dark:bg-violet-900/40 text-violet-800 dark:text-violet-200 px-2 py-0.5 text-[11px] font-medium">
                    P{e.phase || '?'}
                  </span>
                </div>
                <p className="text-[12px] text-slate-500 dark:text-slate-400 leading-relaxed line-clamp-2 min-h-[34px]">
                  {desc}
                  {truncated ? '…' : ''}
                </p>
                <div className="mt-2 flex gap-3 text-[11px] text-slate-500 dark:text-slate-400">
                  <span>{sc} stories</span>
                  <span>{pc} plans</span>
                  {e.engine && <span className="truncate">{e.engine}</span>}
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
