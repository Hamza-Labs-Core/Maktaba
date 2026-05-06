import {
  createHashHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
  useNavigate,
  useParams,
  useSearch,
} from '@tanstack/react-router';
import { Menu, X } from 'lucide-react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from 'react';
import type { EntryType, WikiBundle } from '@/types/wiki';
import { useWikiDB } from '@/hooks/useWikiDB';
import { useSearch as useSearchHits } from '@/hooks/useSearch';
import type { IndexedEntry } from '@/lib/search-index';
import { Sidebar } from '@/components/Sidebar';
import { Dashboard } from '@/components/Dashboard';
import { EntryPage } from '@/components/EntryPage';
import { SearchResults } from '@/components/SearchResults';
import { TypeListPage } from '@/components/TypeListPage';
import { GlossaryPage } from '@/components/GlossaryPage';

//
// ─── Shared app context ──────────────────────────────────────────────
//
interface AppCtx {
  bundle: WikiBundle;
  index: IndexedEntry[];
  theme: 'light' | 'dark';
  toggleTheme: () => void;
  dir: 'ltr' | 'rtl';
  toggleDir: () => void;
  query: string;
  setQuery: (q: string) => void;
  searchInputRef: React.RefObject<HTMLInputElement>;
  sidebarOpen: boolean;
  setSidebarOpen: (v: boolean) => void;
}
const Ctx = createContext<AppCtx | null>(null);
const useApp = (): AppCtx => {
  const c = useContext(Ctx);
  if (!c) throw new Error('useApp() outside provider');
  return c;
};

//
// ─── Route layout (root) ─────────────────────────────────────────────
//
function Layout() {
  const app = useApp();
  return (
    <div className="min-h-screen flex">
      {/* Mobile sidebar scrim */}
      {app.sidebarOpen && (
        <div
          className="fixed inset-0 z-40 bg-black/40 md:hidden"
          onClick={() => app.setSidebarOpen(false)}
        />
      )}

      {/* Sidebar */}
      <div
        className={`fixed inset-y-0 left-0 rtl:left-auto rtl:right-0 z-50 w-72 transform transition-transform md:relative md:translate-x-0 ${
          app.sidebarOpen ? 'translate-x-0' : '-translate-x-full md:translate-x-0 rtl:translate-x-full md:rtl:translate-x-0'
        }`}
      >
        <Sidebar
          bundle={app.bundle}
          theme={app.theme}
          toggleTheme={app.toggleTheme}
          dir={app.dir}
          toggleDir={app.toggleDir}
          query={app.query}
          onQueryChange={app.setQuery}
          searchInputRef={app.searchInputRef}
          onClose={() => app.setSidebarOpen(false)}
        />
      </div>

      {/* Main */}
      <main className="flex-1 min-w-0 flex flex-col">
        <div className="md:hidden flex items-center gap-2 border-b border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 px-4 py-2.5 sticky top-0 z-30">
          <button
            onClick={() => app.setSidebarOpen(!app.sidebarOpen)}
            className="inline-flex items-center justify-center h-8 w-8 rounded border border-slate-200 dark:border-slate-800 hover:bg-slate-100 dark:hover:bg-slate-900 transition-colors"
            aria-label="Toggle sidebar"
          >
            {app.sidebarOpen ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
          </button>
          <div className="flex items-center gap-2">
            <div className="h-7 w-7 rounded-md bg-gradient-to-br from-brand-600 to-violet-500 text-white grid place-items-center font-extrabold text-xs">
              M
            </div>
            <span className="font-bold text-slate-900 dark:text-slate-100">
              Maktaba Wiki
            </span>
          </div>
        </div>
        <div className="flex-1 px-6 sm:px-10 py-8 max-w-5xl w-full mx-auto">
          <Outlet />
        </div>
      </main>
    </div>
  );
}

//
// ─── Route components ────────────────────────────────────────────────
//
function HomeRoute() {
  const { bundle } = useApp();
  return <Dashboard bundle={bundle} />;
}

function EntryRoute() {
  const { id } = useParams({ from: '/entry/$id' });
  const { bundle } = useApp();
  return <EntryPage bundle={bundle} id={id} />;
}

function TypeRoute() {
  const { type } = useParams({ from: '/type/$type' });
  const { bundle } = useApp();
  return <TypeListPage bundle={bundle} type={type as EntryType} />;
}

function SearchRoute() {
  const search = useSearch({ from: '/search' }) as { q?: string };
  const q = search.q || '';
  const { index, query, setQuery } = useApp();
  // Keep sidebar input synced with route q.
  useEffect(() => {
    if (q !== query) setQuery(q);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [q]);
  const hits = useSearchHits(index, q);
  return <SearchResults query={q} hits={hits} />;
}

function GlossaryRoute() {
  const { bundle } = useApp();
  return <GlossaryPage bundle={bundle} />;
}

//
// ─── Router setup ────────────────────────────────────────────────────
//
const rootRoute = createRootRoute({ component: Layout });
const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: HomeRoute,
});
const entryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/entry/$id',
  component: EntryRoute,
});
const typeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/type/$type',
  component: TypeRoute,
});
const searchRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/search',
  validateSearch: (s: Record<string, unknown>) => ({
    q: typeof s.q === 'string' ? s.q : '',
  }),
  component: SearchRoute,
});
const glossaryRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/glossary',
  component: GlossaryRoute,
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  entryRoute,
  typeRoute,
  searchRoute,
  glossaryRoute,
]);

const router = createRouter({
  routeTree,
  history: createHashHistory(),
  defaultPreload: 'intent',
});

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

//
// ─── App ────────────────────────────────────────────────────────────
//
export function App() {
  const { bundle, index, loading, error } = useWikiDB();

  // Theme + RTL
  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    const stored = (typeof localStorage !== 'undefined' &&
      localStorage.getItem('maktaba-wiki-theme')) as 'light' | 'dark' | null;
    if (stored === 'light' || stored === 'dark') return stored;
    return typeof window !== 'undefined' &&
      window.matchMedia?.('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'light';
  });
  const [dir, setDir] = useState<'ltr' | 'rtl'>(() => {
    return ((typeof localStorage !== 'undefined' &&
      localStorage.getItem('maktaba-wiki-dir')) as 'ltr' | 'rtl' | null) || 'ltr';
  });
  useEffect(() => {
    document.documentElement.classList.toggle('dark', theme === 'dark');
    try {
      localStorage.setItem('maktaba-wiki-theme', theme);
    } catch {}
  }, [theme]);
  useEffect(() => {
    document.documentElement.setAttribute('dir', dir);
    try {
      localStorage.setItem('maktaba-wiki-dir', dir);
    } catch {}
  }, [dir]);

  const toggleTheme = useCallback(
    () => setTheme((t) => (t === 'dark' ? 'light' : 'dark')),
    [],
  );
  const toggleDir = useCallback(
    () => setDir((d) => (d === 'ltr' ? 'rtl' : 'ltr')),
    [],
  );

  // Search query (sync with route)
  const [query, setQuery] = useState('');
  const searchInputRef = useRef<HTMLInputElement>(null);

  // Mobile sidebar
  const [sidebarOpen, setSidebarOpen] = useState(false);

  // Keyboard shortcuts
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (
        e.key === '/' &&
        document.activeElement?.tagName !== 'INPUT' &&
        document.activeElement?.tagName !== 'TEXTAREA'
      ) {
        e.preventDefault();
        searchInputRef.current?.focus();
        searchInputRef.current?.select();
      } else if (e.key === 'Escape') {
        if (document.activeElement === searchInputRef.current) {
          searchInputRef.current?.blur();
        } else {
          setSidebarOpen(false);
        }
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Debounced query → navigate to /search
  useEffect(() => {
    const t = setTimeout(() => {
      const q = query.trim();
      const cur = router.state.location.pathname;
      if (!q) {
        if (cur.startsWith('/search')) router.navigate({ to: '/' });
      } else {
        router.navigate({ to: '/search', search: { q } });
      }
    }, 120);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [query]);

  const ctx = useMemo<AppCtx | null>(() => {
    if (!bundle) return null;
    return {
      bundle,
      index,
      theme,
      toggleTheme,
      dir,
      toggleDir,
      query,
      setQuery,
      searchInputRef,
      sidebarOpen,
      setSidebarOpen,
    };
  }, [bundle, index, theme, dir, query, sidebarOpen, toggleTheme, toggleDir]);

  if (loading) {
    return (
      <div className="grid place-items-center min-h-screen text-slate-500 dark:text-slate-400">
        <div className="flex items-center gap-3">
          <div className="h-6 w-6 rounded-full border-2 border-slate-200 dark:border-slate-700 border-t-brand-500 animate-spin" />
          Loading wiki…
        </div>
      </div>
    );
  }

  if (error || !ctx) {
    return (
      <div className="grid place-items-center min-h-screen p-6">
        <div className="max-w-md text-center">
          <h1 className="text-2xl font-bold text-slate-900 dark:text-slate-100 mb-2">
            Could not load wiki
          </h1>
          <p className="text-slate-600 dark:text-slate-400 mb-4">{error}</p>
          <p className="text-sm text-slate-500">
            Run <code className="font-mono bg-slate-100 dark:bg-slate-800 px-1.5 py-0.5 rounded">npm run dev</code> from <code className="font-mono">web/wiki-app/</code>.
          </p>
        </div>
      </div>
    );
  }

  return (
    <Ctx.Provider value={ctx}>
      <RouterProvider router={router} />
    </Ctx.Provider>
  );
}

// Bridge from sidebar Search Enter key — also update local query.
export function useNavigateToSearch() {
  const navigate = useNavigate();
  return (q: string) => navigate({ to: '/search', search: { q } });
}
