// The wiki in English and Arabic. The language comes from the route: maktaba.dev/wiki/ is
// English, maktaba.dev/ar/wiki/ is Arabic, right to left (and `?lang=ar` on the dev server).
// The Arabic page reads the Arabic database (web/wiki-app/public/db/ar/, which
// docs/wiki/db/generate_wiki.py writes from the translation store); every string the app
// itself shows is below, in both languages. `AR` has every key `EN` has, or the build fails.

export type Lang = 'en' | 'ar';

/** The page's language: from its path (/ar/wiki/…), else `?lang=ar`. */
export function langFrom(pathname: string, search = ''): Lang {
  if (/^\/ar(\/|$)/.test(pathname)) return 'ar';
  return new URLSearchParams(search).get('lang') === 'ar' ? 'ar' : 'en';
}

export const LANG: Lang =
  typeof window === 'undefined' ? 'en' : langFrom(window.location.pathname, window.location.search);
export const DIR: 'ltr' | 'rtl' = LANG === 'ar' ? 'rtl' : 'ltr';

/**
 * The database's folder for a language. The bundle is built for /wiki/ (`base`): the English
 * database is /wiki/db/, the Arabic one /ar/wiki/db/. On the dev server (base ./) the Arabic
 * database is db/ar/ beside the English one, as in public/.
 */
export function dbBase(lang: Lang, base: string): string {
  const b = base.endsWith('/') ? base : `${base}/`;
  if (lang === 'en') return `${b}db`;
  return b.startsWith('/') ? `/ar${b}db` : `${b}db/ar`;
}

/** The same page in the other language: /wiki/#/x <-> /ar/wiki/#/x (or ?lang= on dev). */
export function otherLangHref(lang: Lang, loc: { pathname: string; search: string; hash: string }): string {
  const { pathname, search, hash } = loc;
  if (/^\/(ar\/)?wiki(\/|$)/.test(pathname)) {
    const p = lang === 'ar' ? pathname.replace(/^\/ar/, '') : `/ar${pathname}`;
    return `${p}${search}${hash}`;
  }
  const q = new URLSearchParams(search);
  if (lang === 'ar') q.delete('lang');
  else q.set('lang', 'ar');
  const s = q.toString();
  return `${pathname}${s ? `?${s}` : ''}${hash}`;
}

export const EN = {
  'app.name': 'Maktaba Wiki',
  'app.short': 'Wiki',
  'app.loading': 'Loading the wiki…',
  'app.error.title': 'Could not load the wiki',
  'app.error.body': 'Could not load the wiki database from',
  'app.error.hint': 'Run npm run dev from web/wiki-app/.',
  'lang.switch': 'العربية',
  'lang.switch.label': 'اقرأ الويكي بالعربية',
  'theme.toggle': 'Switch the theme',
  'theme.dark': 'Dark',
  'theme.light': 'Light',
  'sidebar.toggle': 'Show or hide the menu',
  'search.placeholder': 'Search the wiki…',
  'search.title': 'Search',
  'search.empty': 'Type to search every wiki entry.',
  'search.found.one': 'Found 1 result for',
  'search.found.many': 'Found {n} results for',
  'search.none': 'No matches.',
  'nav.external': 'Elsewhere',
  'nav.issues': 'Issues',
  'nav.issues.hint': 'GitHub',
  'nav.repo.hint': 'source',
  'nav.overview': 'Overview',
  'nav.browse': 'Browse',
  'nav.glossary': 'Glossary',
  'nav.allStories': 'All stories',
  'nav.epics': '{n} epics',
  'home': 'Home',
  'dash.issues': 'GitHub issues',
  'dash.repo': 'GitHub repo',
  'dash.phases': 'Phases',
  'dash.allEpics': 'All epics',
  'dash.stories': '{n} stories',
  'dash.plans': '{n} plans',
  'dash.pctStories': '{n}% of stories',
  'dash.stat.epic': 'Epics',
  'dash.stat.story': 'Stories',
  'dash.stat.plan': 'Plans',
  'dash.stat.endpoint': 'API endpoints',
  'dash.stat.entity': 'DB tables',
  'dash.stat.diagram': 'Diagrams',
  'dash.stat.mockup': 'Mockups',
  'dash.stat.review': 'Reviews',
  'entry.notFound': 'Not found',
  'entry.notFound.body': 'No entry has the id',
  'entry.back': 'Back to the overview',
  'entry.phase': 'Phase {n}',
  'entry.epic': 'Epic {n}',
  'entry.mockup': 'Mockup',
  'entry.mockups': 'Mockups ({n})',
  'entry.diagram': 'Diagram',
  'entry.diagrams': 'Diagrams ({n})',
  'entry.files': 'Files',
  'entry.endpoints': 'API endpoints',
  'entry.tables': 'Database tables',
  'entry.dbTable': 'DB table',
  'entry.stories': 'Stories ({n})',
  'entry.plans': 'Plans ({n})',
  'entry.related': 'Related',
  'list.entries.one': '1 entry',
  'list.entries.many': '{n} entries',
  'list.none': 'No entries.',
  'glossary.title': 'Glossary',
  'glossary.lead': 'Database tables and API endpoints',
  'glossary.tables': 'DB tables ({n})',
  'glossary.endpoints': 'API endpoints ({n})',
  'glossary.entity': 'table',
  'viewer.source': 'Source',
  'viewer.source.title': 'View the source on GitHub',
  'viewer.open': 'Open',
  'viewer.open.title': 'Open the mockup in a new tab',
  'viewer.raw': 'Raw',
  'viewer.raw.title': 'Open the raw .drawio file',
  'viewer.badPath': 'Cannot resolve the mockup path:',
  'viewer.loading': 'Loading the diagram…',
  'viewer.missing': 'The diagram file is not committed yet.',
  'viewer.missing.body': 'Expected at',
  'viewer.error': 'Could not load the diagram:',
  'type.epic': 'Epic',
  'type.story': 'Story',
  'type.plan': 'Plan',
  'type.diagram': 'Diagram',
  'type.review': 'Review',
  'type.mockup': 'Mockup',
  'type.endpoint': 'Endpoint',
  'type.entity': 'Entity',
  'type.feature': 'Feature',
  'type.phase': 'Phase',
  'types.epic': 'Epics',
  'types.story': 'Stories',
  'types.plan': 'Plans',
  'types.diagram': 'Diagrams',
  'types.review': 'Reviews',
  'types.mockup': 'Mockups',
  'types.endpoint': 'API endpoints',
  'types.entity': 'Entities',
  'types.feature': 'Features',
  'types.phase': 'Phases',
  'file.readme': 'readme',
  'file.story': 'story',
  'file.plan': 'plan',
  'file.mockups': 'mockups',
  'file.mockup': 'mockup',
  'file.diagram': 'diagram',
  'file.review': 'review',
  'file.openapi': 'openapi',
  'file.schema': 'schema',
  'file.manifest': 'manifest',
} as const;

export type Key = keyof typeof EN;

export const AR: Record<Key, string> = {
  'app.name': 'ويكي مكتبة',
  'app.short': 'الويكي',
  'app.loading': 'جارٍ تحميل الويكي…',
  'app.error.title': 'تعذّر تحميل الويكي',
  'app.error.body': 'تعذّر تحميل قاعدة بيانات الويكي من',
  'app.error.hint': 'شغّل npm run dev من web/wiki-app/.',
  'lang.switch': 'English',
  'lang.switch.label': 'Read the wiki in English',
  'theme.toggle': 'بدّل السمة',
  'theme.dark': 'داكنة',
  'theme.light': 'فاتحة',
  'sidebar.toggle': 'أظهر القائمة أو أخفها',
  'search.placeholder': 'ابحث في الويكي…',
  'search.title': 'البحث',
  'search.empty': 'اكتب للبحث في كل مدخلات الويكي.',
  'search.found.one': 'نتيجة واحدة لـ',
  'search.found.many': '{n} نتيجة لـ',
  'search.none': 'لا نتائج.',
  'nav.external': 'روابط خارجية',
  'nav.issues': 'المشكلات',
  'nav.issues.hint': 'GitHub',
  'nav.repo.hint': 'الشيفرة',
  'nav.overview': 'نظرة عامة',
  'nav.browse': 'تصفّح',
  'nav.glossary': 'المسرد',
  'nav.allStories': 'كل القصص',
  'nav.epics': 'الملاحم: {n}',
  'home': 'الرئيسية',
  'dash.issues': 'مشكلات GitHub',
  'dash.repo': 'المستودع على GitHub',
  'dash.phases': 'المراحل',
  'dash.allEpics': 'كل الملاحم',
  'dash.stories': 'القصص: {n}',
  'dash.plans': 'الخطط: {n}',
  'dash.pctStories': '{n}% من القصص',
  'dash.stat.epic': 'الملاحم',
  'dash.stat.story': 'القصص',
  'dash.stat.plan': 'الخطط',
  'dash.stat.endpoint': 'نقاط وصول الواجهة البرمجية',
  'dash.stat.entity': 'جداول قاعدة البيانات',
  'dash.stat.diagram': 'الرسوم المعمارية',
  'dash.stat.mockup': 'نماذج الواجهات',
  'dash.stat.review': 'المراجعات',
  'entry.notFound': 'غير موجود',
  'entry.notFound.body': 'لا يوجد مدخل بالمعرّف',
  'entry.back': 'العودة إلى النظرة العامة',
  'entry.phase': 'المرحلة {n}',
  'entry.epic': 'الملحمة {n}',
  'entry.mockup': 'نموذج الواجهة',
  'entry.mockups': 'نماذج الواجهات ({n})',
  'entry.diagram': 'الرسم المعماري',
  'entry.diagrams': 'الرسوم المعمارية ({n})',
  'entry.files': 'الملفات',
  'entry.endpoints': 'نقاط وصول الواجهة البرمجية',
  'entry.tables': 'جداول قاعدة البيانات',
  'entry.dbTable': 'جدول قاعدة بيانات',
  'entry.stories': 'القصص ({n})',
  'entry.plans': 'الخطط ({n})',
  'entry.related': 'ذو صلة',
  'list.entries.one': 'مدخل واحد',
  'list.entries.many': 'المدخلات: {n}',
  'list.none': 'لا مدخلات.',
  'glossary.title': 'المسرد',
  'glossary.lead': 'جداول قاعدة البيانات ونقاط وصول الواجهة البرمجية',
  'glossary.tables': 'جداول قاعدة البيانات ({n})',
  'glossary.endpoints': 'نقاط وصول الواجهة البرمجية ({n})',
  'glossary.entity': 'جدول',
  'viewer.source': 'المصدر',
  'viewer.source.title': 'اعرض المصدر على GitHub',
  'viewer.open': 'افتح',
  'viewer.open.title': 'افتح نموذج الواجهة في لسان جديد',
  'viewer.raw': 'الملف الخام',
  'viewer.raw.title': 'افتح ملف ‎.drawio الخام',
  'viewer.badPath': 'تعذّر تحديد مسار نموذج الواجهة:',
  'viewer.loading': 'جارٍ تحميل الرسم…',
  'viewer.missing': 'ملف الرسم لم يُضف إلى المستودع بعد.',
  'viewer.missing.body': 'المتوقع في',
  'viewer.error': 'تعذّر تحميل الرسم:',
  'type.epic': 'ملحمة',
  'type.story': 'قصة',
  'type.plan': 'خطة',
  'type.diagram': 'رسم معماري',
  'type.review': 'مراجعة',
  'type.mockup': 'نموذج واجهة',
  'type.endpoint': 'نقطة وصول',
  'type.entity': 'جدول',
  'type.feature': 'ميزة',
  'type.phase': 'مرحلة',
  'types.epic': 'الملاحم',
  'types.story': 'القصص',
  'types.plan': 'الخطط',
  'types.diagram': 'الرسوم المعمارية',
  'types.review': 'المراجعات',
  'types.mockup': 'نماذج الواجهات',
  'types.endpoint': 'نقاط وصول الواجهة البرمجية',
  'types.entity': 'الجداول',
  'types.feature': 'الميزات',
  'types.phase': 'المراحل',
  'file.readme': 'الملحمة',
  'file.story': 'القصة',
  'file.plan': 'الخطة',
  'file.mockups': 'نماذج الواجهات',
  'file.mockup': 'نموذج الواجهة',
  'file.diagram': 'الرسم',
  'file.review': 'المراجعة',
  'file.openapi': 'وصف OpenAPI',
  'file.schema': 'المخطط',
  'file.manifest': 'البيان',
};

export const STRINGS: Record<Lang, Record<Key, string>> = { en: EN, ar: AR };

/** A string in the page's language, with {n} filled in. */
export function t(key: Key, vars: Record<string, string | number> = {}, lang: Lang = LANG): string {
  return STRINGS[lang][key].replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
}

/** A file role's label (`files` keys in the database), or the role itself. */
export function fileRole(role: string, lang: Lang = LANG): string {
  const k = `file.${role}` as Key;
  return k in EN ? STRINGS[lang][k] : role;
}
