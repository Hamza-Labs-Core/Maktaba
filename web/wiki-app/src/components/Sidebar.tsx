import { Link, useLocation, useNavigate } from '@tanstack/react-router';
import {
  BookMarked,
  ChevronRight,
  CircleDot,
  Database,
  ExternalLink,
  FileText,
  GitBranch,
  Github,
  Grid3x3,
  Home,
  Image,
  Layers,
  ListChecks,
  Moon,
  Search,
  Sparkles,
  Sun,
} from 'lucide-react';
import {
  KeyboardEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { EntryType, WikiBundle, WikiEntry } from '@/types/wiki';
import {
  GITHUB_ISSUES,
  GITHUB_REPO,
  PHASES,
  TYPE_LABEL_PLURAL,
  phaseText,
} from '@/lib/labels';
import { t } from '@/lib/i18n';
import { LangSwitch } from './LangSwitch';

interface Props {
  bundle: WikiBundle;
  theme: 'light' | 'dark';
  toggleTheme: () => void;
  query: string;
  onQueryChange: (q: string) => void;
  searchInputRef: React.RefObject<HTMLInputElement>;
  onClose?: () => void;
}

export function Sidebar({
  bundle,
  theme,
  toggleTheme,
  query,
  onQueryChange,
  searchInputRef,
  onClose,
}: Props) {
  const navigate = useNavigate();
  const location = useLocation();
  const epics = bundle.byType.get('epic') || [];

  const phases = useMemo(() => {
    const m = new Map<number, WikiEntry[]>();
    for (const e of epics) {
      const ph = e.phase ?? 0;
      const arr = m.get(ph) || [];
      arr.push(e);
      m.set(ph, arr);
    }
    return m;
  }, [epics]);

  const onSearchKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      e.currentTarget.blur();
      onQueryChange('');
      if (location.pathname.startsWith('/search')) navigate({ to: '/' });
    } else if (e.key === 'Enter') {
      const q = e.currentTarget.value.trim();
      if (q) navigate({ to: '/search', search: { q } });
    }
  };

  return (
    <aside className="flex h-screen w-full flex-col overflow-hidden border-e border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-950">
      {/* Header */}
      <div className="border-b border-slate-200 dark:border-slate-800 p-4 sticky top-0 bg-slate-50 dark:bg-slate-950 z-10">
        <Link
          to="/"
          onClick={onClose}
          className="flex items-center gap-2.5 mb-3 hover:opacity-80 transition-opacity"
        >
          <div className="h-8 w-8 rounded-lg bg-gradient-to-br from-brand-600 to-violet-500 text-white grid place-items-center font-extrabold text-sm shadow-soft">
            M
          </div>
          <div className="leading-tight">
            <div className="text-sm font-bold text-slate-900 dark:text-slate-100">
              {t('app.name')}
            </div>
            <div className="text-[11px] text-slate-500 dark:text-slate-400">
              {t('app.short')}
            </div>
          </div>
        </Link>
        <div className="relative">
          <Search className="absolute start-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-slate-400" />
          <input
            ref={searchInputRef}
            value={query}
            onChange={(e) => onQueryChange(e.target.value)}
            onKeyDown={onSearchKeyDown}
            type="search"
            placeholder={t('search.placeholder')}
            aria-label={t('search.title')}
            className="w-full rounded-md border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 py-1.5 ps-8 pe-9 text-[13px] text-slate-900 dark:text-slate-100 placeholder:text-slate-400 focus:border-brand-500 focus:ring-1 focus:ring-brand-500 outline-none"
          />
          <kbd className="absolute end-2 top-1/2 -translate-y-1/2 rounded border border-slate-200 dark:border-slate-700 bg-slate-100 dark:bg-slate-800 px-1.5 py-px text-[10px] text-slate-500 dark:text-slate-400 font-mono">
            /
          </kbd>
        </div>
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto p-2 text-[13px]">
        {/* External */}
        <Section heading={t('nav.external')}>
          <ExternalRow href={GITHUB_ISSUES} icon={<CircleDot className="h-3.5 w-3.5" />} label={t('nav.issues')} hint={t('nav.issues.hint')} />
          <ExternalRow href={GITHUB_REPO} icon={<Github className="h-3.5 w-3.5" />} label="GitHub" hint={t('nav.repo.hint')} />
        </Section>

        {/* Home */}
        <Section>
          <NavItem to="/" icon={<Home className="h-3.5 w-3.5" />} label={t('nav.overview')} onClose={onClose} />
        </Section>

        {/* Phases & epics */}
        {PHASES.map((ph) => {
          const phaseEpics = phases.get(ph);
          if (!phaseEpics || !phaseEpics.length) return null;
          const phaseStoryCount = phaseEpics.reduce(
            (sum, e) => sum + (Number(e.metadata?.story_count) || 0),
            0,
          );
          return (
            <Section heading={phaseText(ph).title} key={ph}>
              <CollapsibleGroup
                label={t('nav.epics', { n: phaseEpics.length })}
                count={phaseStoryCount}
                defaultOpen={ph === 1}
              >
                {phaseEpics.map((epic) => {
                  const stories = bundle.epicStories.get(epic.id) || [];
                  return (
                    <CollapsibleGroup
                      key={epic.id}
                      label={epic.title}
                      count={
                        Number(epic.metadata?.story_count) || stories.length
                      }
                    >
                      <NavItem
                        to="/entry/$id"
                        params={{ id: epic.id }}
                        icon={<BookMarked className="h-3.5 w-3.5" />}
                        label={t('nav.overview')}
                        onClose={onClose}
                      />
                      {stories.map((s) => (
                        <NavItem
                          key={s.id}
                          to="/entry/$id"
                          params={{ id: s.id }}
                          icon={<FileText className="h-3.5 w-3.5" />}
                          label={s.title}
                          onClose={onClose}
                        />
                      ))}
                    </CollapsibleGroup>
                  );
                })}
              </CollapsibleGroup>
            </Section>
          );
        })}

        {/* Browse */}
        <Section heading={t('nav.browse')}>
          <BrowseRow type="feature" icon={<Sparkles className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="entity" icon={<Database className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="endpoint" icon={<GitBranch className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="diagram" icon={<Grid3x3 className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="mockup" icon={<Image className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="review" icon={<ListChecks className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="plan" icon={<Layers className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} />
          <BrowseRow type="story" icon={<FileText className="h-3.5 w-3.5" />} bundle={bundle} onClose={onClose} label={t('nav.allStories')} />
          <NavItem
            to="/glossary"
            icon={<BookMarked className="h-3.5 w-3.5" />}
            label={t('nav.glossary')}
            onClose={onClose}
          />
        </Section>
      </nav>

      {/* Footer */}
      <div className="flex items-center gap-2 border-t border-slate-200 dark:border-slate-800 p-2.5 bg-slate-50 dark:bg-slate-950">
        <button
          onClick={toggleTheme}
          title={t('theme.toggle')}
          className="inline-flex items-center gap-1.5 rounded border border-slate-200 dark:border-slate-700 px-2 py-1 text-[11px] text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
        >
          {theme === 'dark' ? <Moon className="h-3 w-3" /> : <Sun className="h-3 w-3" />}
          {theme === 'dark' ? t('theme.dark') : t('theme.light')}
        </button>
        <LangSwitch />
        <a
          href={GITHUB_REPO}
          target="_blank"
          rel="noopener noreferrer"
          aria-label="GitHub"
          className="ms-auto inline-flex items-center gap-1 rounded border border-slate-200 dark:border-slate-700 px-2 py-1 text-[11px] text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
        >
          <Github className="h-3 w-3" />
        </a>
      </div>
    </aside>
  );
}

function Section({
  heading,
  children,
}: {
  heading?: React.ReactNode;
  children: React.ReactNode;
}) {
  return (
    <div className="mb-1">
      {heading && (
        <div className="px-3 pt-3 pb-1.5 text-[10px] font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-500 rtl:normal-case rtl:tracking-normal rtl:text-[11px]">
          {heading}
        </div>
      )}
      {children}
    </div>
  );
}

function NavItem({
  to,
  params,
  icon,
  label,
  badge,
  onClose,
}: {
  to: string;
  params?: Record<string, string>;
  icon?: React.ReactNode;
  label: string;
  badge?: number;
  onClose?: () => void;
}) {
  return (
    <Link
      to={to}
      params={params}
      onClick={onClose}
      className="flex items-center gap-2 px-2.5 py-1.5 rounded text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800/60 hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
      activeOptions={{ exact: to === '/' }}
      activeProps={{
        className:
          'flex items-center gap-2 px-2.5 py-1.5 rounded bg-brand-50 dark:bg-brand-900/40 text-brand-800 dark:text-brand-200 font-medium',
      }}
    >
      {icon && <span className="opacity-70 shrink-0">{icon}</span>}
      <span className="flex-1 truncate">{label}</span>
      {badge != null && (
        <span className="rounded-full bg-slate-200 dark:bg-slate-800 px-2 py-px text-[10px] font-medium text-slate-700 dark:text-slate-300">
          {badge}
        </span>
      )}
    </Link>
  );
}

function ExternalRow({
  href,
  icon,
  label,
  hint,
}: {
  href: string;
  icon: React.ReactNode;
  label: string;
  hint: string;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center gap-2 px-2.5 py-1.5 rounded text-[12px] text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800/60 hover:text-slate-900 dark:hover:text-slate-100 transition-colors"
    >
      <span className="opacity-70">{icon}</span>
      <span className="flex-1">{label}</span>
      <span className="text-[10px] text-slate-400">{hint}</span>
      <ExternalLink className="h-3 w-3 opacity-50" />
    </a>
  );
}

function CollapsibleGroup({
  label,
  count,
  defaultOpen = false,
  children,
}: {
  label: string;
  count?: number;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  // Auto-open if a child link becomes active (best-effort: rely on hash url change).
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (ref.current?.querySelector('.bg-brand-50, .dark\\:bg-brand-900\\/40')) {
      setOpen(true);
    }
  });
  return (
    <div ref={ref}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="w-full flex items-center gap-2 px-2.5 py-1.5 rounded text-slate-600 dark:text-slate-300 hover:bg-slate-200/60 dark:hover:bg-slate-800/60 hover:text-slate-900 dark:hover:text-slate-100 transition-colors text-start"
      >
        <ChevronRight
          className={`h-3 w-3 shrink-0 transition-transform ${open ? 'rotate-90' : 'rtl:rotate-180'}`}
        />
        <span className="flex-1 truncate font-medium">{label}</span>
        {count != null && (
          <span className="rounded-full bg-slate-200 dark:bg-slate-800 px-2 py-px text-[10px] font-medium text-slate-700 dark:text-slate-300">
            {count}
          </span>
        )}
      </button>
      {open && <div className="ms-3 border-s border-slate-200 dark:border-slate-800 ps-1 mt-0.5 mb-1">{children}</div>}
    </div>
  );
}

function BrowseRow({
  type,
  icon,
  bundle,
  onClose,
  label,
}: {
  type: EntryType;
  icon: React.ReactNode;
  bundle: WikiBundle;
  onClose?: () => void;
  label?: string;
}) {
  const arr = bundle.byType.get(type) || [];
  return (
    <NavItem
      to="/type/$type"
      params={{ type }}
      icon={icon}
      label={label || TYPE_LABEL_PLURAL[type]}
      badge={arr.length}
      onClose={onClose}
    />
  );
}
