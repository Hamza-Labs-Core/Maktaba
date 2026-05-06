import type { EntryType } from '@/types/wiki';

export const TYPE_LABEL: Record<EntryType, string> = {
  epic: 'Epic',
  story: 'Story',
  plan: 'Plan',
  diagram: 'Diagram',
  review: 'Review',
  mockup: 'Mockup',
  endpoint: 'Endpoint',
  entity: 'Entity',
  feature: 'Feature',
};

export const TYPE_LABEL_PLURAL: Record<EntryType, string> = {
  epic: 'Epics',
  story: 'Stories',
  plan: 'Plans',
  diagram: 'Diagrams',
  review: 'Reviews',
  mockup: 'Mockups',
  endpoint: 'API Endpoints',
  entity: 'Entities',
  feature: 'Features',
};

export const TYPE_CHIP_CLASS: Record<EntryType, string> = {
  epic: 'bg-violet-100 text-violet-800 dark:bg-violet-900/40 dark:text-violet-200',
  story: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200',
  plan: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200',
  diagram: 'bg-orange-100 text-orange-800 dark:bg-orange-900/40 dark:text-orange-200',
  review: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  mockup: 'bg-pink-100 text-pink-800 dark:bg-pink-900/40 dark:text-pink-200',
  endpoint: 'bg-cyan-100 text-cyan-800 dark:bg-cyan-900/40 dark:text-cyan-200',
  entity: 'bg-purple-100 text-purple-800 dark:bg-purple-900/40 dark:text-purple-200',
  feature: 'bg-rose-100 text-rose-800 dark:bg-rose-900/40 dark:text-rose-200',
};

export const METHOD_CHIP_CLASS: Record<string, string> = {
  GET: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200',
  POST: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200',
  PUT: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  PATCH: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  DELETE: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-200',
};

export const PHASE_INFO: Record<
  number,
  { title: string; sub: string; range: string }
> = {
  1: {
    title: 'Phase 1 · Pipeline',
    sub: 'Scanner → Audio → Transcription → Subtitles → Search → Job Queue',
    range: 'Epics 01–06',
  },
  2: {
    title: 'Phase 2 · API & Streaming',
    sub: 'API server, streaming, library mgmt, auth & security',
    range: 'Epics 07–10',
  },
  3: {
    title: 'Phase 3 · Clients',
    sub: 'Web, mobile, desktop, TV, discovery, subscriptions, design',
    range: 'Epics 11–17',
  },
  4: {
    title: 'Phase 4 · Hardening',
    sub: 'Performance, scalability, testing, observability, devops, security',
    range: 'Epics 18–24',
  },
};

export const GITHUB_REPO = 'https://github.com/Hamza-Labs-Core/Maktaba';
export const GITHUB_BRANCH = 'main';
export const LINEAR_PROJECT =
  'https://linear.app/hamzalabs/project/maktaba-129e338d2e41';
export const LINEAR_ISSUE_BASE = 'https://linear.app/hamzalabs/issue/';
export const PENPOT_LINK = '#'; // placeholder — fill in when Penpot project URL is known

export function githubUrlFor(path: string): string {
  if (!path) return '#';
  const clean = path.replace(/^\/+/, '');
  return `${GITHUB_REPO}/blob/${GITHUB_BRANCH}/${clean}`;
}

export function endpointIdFor(ep: string): string | null {
  if (!ep) return null;
  const parts = ep.trim().split(/\s+/);
  if (parts.length < 2) return null;
  const [method, ...rest] = parts;
  let path = rest.join(' ').toLowerCase();
  path = path.replace(/[{}]/g, '');
  path = path.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  return `ep-${method.toLowerCase()}-${path}`;
}
