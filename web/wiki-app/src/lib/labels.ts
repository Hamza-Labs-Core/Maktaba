import type { EntryType } from '@/types/wiki';
import { LANG, t } from './i18n';

const TYPES: EntryType[] = [
  'epic',
  'story',
  'plan',
  'diagram',
  'review',
  'mockup',
  'endpoint',
  'entity',
  'feature',
  'phase',
];

// in the page's language (lib/i18n.ts)
export const TYPE_LABEL = Object.fromEntries(
  TYPES.map((x) => [x, t(`type.${x}`)]),
) as Record<EntryType, string>;

export const TYPE_LABEL_PLURAL = Object.fromEntries(
  TYPES.map((x) => [x, t(`types.${x}`)]),
) as Record<EntryType, string>;

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
  phase: 'bg-teal-100 text-teal-800 dark:bg-teal-900/40 dark:text-teal-200',
};

export const METHOD_CHIP_CLASS: Record<string, string> = {
  GET: 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200',
  POST: 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200',
  PUT: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  PATCH: 'bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200',
  DELETE: 'bg-red-100 text-red-800 dark:bg-red-900/40 dark:text-red-200',
};

// The epic groups the sidebar and the dashboard show (an epic's `phase`), each in English and
// Arabic. docs/wiki/db/curated.json sets an epic's phase; an epic it does not name is in 5.
export interface PhaseInfo {
  title: string;
  titleAr: string;
  sub: string;
  subAr: string;
  range: string;
  rangeAr: string;
}

export const PHASE_INFO: Record<number, PhaseInfo> = {
  1: {
    title: 'Phase 1 · Pipeline',
    titleAr: 'المرحلة 1 · خط المعالجة',
    sub: 'Scanner → Audio → Transcription → Subtitles → Search → Job Queue',
    subAr: 'الماسح ← الصوت ← التفريغ النصي ← الترجمات ← البحث ← طابور المهام',
    range: 'Epics 01–06',
    rangeAr: 'الملاحم 01–06',
  },
  2: {
    title: 'Phase 2 · API & Streaming',
    titleAr: 'المرحلة 2 · الواجهة البرمجية والبث',
    sub: 'API server, streaming, library mgmt, auth & security',
    subAr: 'خادم الواجهة البرمجية، البث، إدارة المكتبات، تسجيل الدخول والأمان',
    range: 'Epics 07–10',
    rangeAr: 'الملاحم 07–10',
  },
  3: {
    title: 'Phase 3 · Clients',
    titleAr: 'المرحلة 3 · التطبيقات',
    sub: 'Web, mobile, desktop, TV, discovery, subscriptions, design',
    subAr: 'الويب، الجوال، سطح المكتب، التلفاز، الاكتشاف، الاشتراكات، التصميم',
    range: 'Epics 11–17',
    rangeAr: 'الملاحم 11–17',
  },
  4: {
    title: 'Phase 4 · Hardening',
    titleAr: 'المرحلة 4 · التحصين',
    sub: 'Performance, scalability, testing, observability, devops, security',
    subAr: 'الأداء، التوسع، الاختبار، المراقبة، التشغيل، الأمان',
    range: 'Epics 18–24',
    rangeAr: 'الملاحم 18–24',
  },
  5: {
    title: 'Phase 5 · Platform',
    titleAr: 'المرحلة 5 · المنصة',
    sub: 'Cloud, live TV, the apps, machines, queue health, search, plugins',
    subAr: 'السحابة، التلفاز المباشر، التطبيقات، الأجهزة، صحة الطوابير، البحث، الإضافات',
    range: 'Epics 25 onward',
    rangeAr: 'الملاحم من 25 فصاعدًا',
  },
};

/** A phase's title, line and range in the page's language. */
export function phaseText(ph: number): { title: string; sub: string; range: string } {
  const p = PHASE_INFO[ph];
  return LANG === 'ar'
    ? { title: p.titleAr, sub: p.subAr, range: p.rangeAr }
    : { title: p.title, sub: p.sub, range: p.range };
}

export const PHASES: number[] = Object.keys(PHASE_INFO)
  .map(Number)
  .sort((a, b) => a - b);

export const GITHUB_REPO = 'https://github.com/Hamza-Labs-Core/Maktaba';
export const GITHUB_BRANCH = 'main';
// the project's public tracker: GitHub issues
export const GITHUB_ISSUES = `${GITHUB_REPO}/issues`;

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
