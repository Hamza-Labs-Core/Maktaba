// Epic 57 family settings: the things a rule can name, for the pages that pick one
// or show one. A rule holds a kind and an id only, so names are read from the
// routes every signed-in person has (their own view: the home lists only what they
// may open):
//
//   library          GET /api/libraries/summary
//   collection       GET /api/collections?library=   (one library's)
//   series           GET /api/series?sort=name
//   channel          GET /api/channels
//   youtube_source   GET /api/libraries/{id}/youtube/sources   (one library's)
//   plugin           GET /api/plugins
//   video            GET /api/videos?q=              (by its title)
//
// A list the home refuses this person is an empty one: the picker says there is
// nothing to choose, and a rule's name falls back to its kind alone.
import { useEffect, useState } from "react";
import { Select } from "@ds/components/Select/Select";
import { Input } from "@ds/components/Input/Input";
import { api } from "../../lib/api";
import { useI18n } from "../../lib/i18n";
import { fetchLibrarySummary } from "../../lib/browse";
import { fetchCollections } from "../../lib/collections";
import { videoTitle } from "../../lib/videoTitle";
import { SUBJECT_KINDS, type SubjectKind } from "../../lib/familyApi";

export interface Option {
  id: string;
  name: string;
}

const obj = (v: unknown): Record<string, unknown> =>
  v && typeof v === "object" ? (v as Record<string, unknown>) : {};
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const str = (v: unknown) => (typeof v === "string" ? v : "");
const enc = encodeURIComponent;

function options(items: unknown, name: (o: Record<string, unknown>) => string): Option[] {
  return list(items)
    .map((v) => {
      const o = obj(v);
      return { id: str(o.id), name: name(o) };
    })
    .filter((o) => o.id !== "");
}

/** Whether choosing a subject of this kind starts with a library. */
export const needsLibrary = (kind: SubjectKind) =>
  kind === "collection" || kind === "youtube_source";
/** Whether a subject of this kind is found by typing its name. */
export const needsQuery = (kind: SubjectKind) => kind === "video";
/** The fewest letters a video search takes. */
export const MIN_QUERY = 2;

/** The subjects of `kind` this person may choose. */
export async function loadOptions(
  kind: SubjectKind,
  ctx: { libraryId: string; query: string; locale: string }
): Promise<Option[]> {
  switch (kind) {
    case "library":
      return (await fetchLibrarySummary()).map((l) => ({ id: l.id, name: l.name }));
    case "collection":
      if (!ctx.libraryId) return [];
      return (await fetchCollections(ctx.libraryId)).map((c) => ({ id: c.id, name: c.name }));
    case "series":
      return options(obj(await api.get<unknown>("/api/series?sort=name")).items, (o) =>
        str(o.name)
      );
    case "channel":
      return options(obj(await api.get<unknown>("/api/channels")).items, (o) => str(o.name));
    case "youtube_source":
      if (!ctx.libraryId) return [];
      return options(
        obj(await api.get<unknown>(`/api/libraries/${enc(ctx.libraryId)}/youtube/sources`)).items,
        (o) => str(o.folder) || str(o.url)
      );
    case "plugin":
      return options(obj(await api.get<unknown>("/api/plugins")).plugins, (o) => {
        const names = obj(obj(o.names).name);
        return str(names[ctx.locale]) || str(names.en) || str(o.id);
      });
    case "video": {
      const q = ctx.query.trim();
      if (q.length < MIN_QUERY) return [];
      return options(obj(await api.get<unknown>(`/api/videos?q=${enc(q)}&limit=20`)).items, (o) =>
        videoTitle({ title: str(o.title), filename: str(o.filename), id: str(o.id) })
      );
    }
  }
}

export interface Picked {
  kind: SubjectKind;
  id: string;
  name: string;
}

/** Picks one thing a rule can name: its kind, then the thing. */
export function SubjectPicker({
  value,
  onChange,
}: {
  value: Picked | null;
  onChange: (next: Picked | null) => void;
}) {
  const { t, locale } = useI18n();
  const [kind, setKind] = useState<SubjectKind>(value?.kind ?? "library");
  const [libraries, setLibraries] = useState<Option[]>([]);
  const [libraryId, setLibraryId] = useState("");
  const [query, setQuery] = useState("");
  const [found, setFound] = useState<Option[] | null>(null);

  useEffect(() => {
    let alive = true;
    void fetchLibrarySummary()
      .then((ls) => alive && setLibraries(ls.map((l) => ({ id: l.id, name: l.name }))))
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, []);

  useEffect(() => {
    setFound(null);
    let alive = true;
    // a typed name waits for the typing to pause
    const timer = window.setTimeout(
      () => {
        void loadOptions(kind, { libraryId, query, locale })
          .catch(() => [] as Option[])
          .then((o) => alive && setFound(o));
      },
      needsQuery(kind) ? 300 : 0
    );
    return () => {
      alive = false;
      window.clearTimeout(timer);
    };
  }, [kind, libraryId, query, locale]);

  const waitsForLibrary = needsLibrary(kind) && !libraryId;
  const waitsForQuery = needsQuery(kind) && query.trim().length < MIN_QUERY;
  const sorted = found
    ? needsQuery(kind)
      ? found
      : [...found].sort((a, b) => a.name.localeCompare(b.name, locale))
    : [];
  return (
    <div className="mkt-parental-picker" data-testid="subject-picker">
      <Select
        label={t("family.rule.kind")}
        value={kind}
        onChange={(e) => {
          setKind(e.target.value as SubjectKind);
          onChange(null);
        }}
      >
        {SUBJECT_KINDS.map((k) => (
          <option key={k} value={k}>
            {t(`family.kind.${k}`)}
          </option>
        ))}
      </Select>
      {needsLibrary(kind) && (
        <Select
          label={t("family.rule.library")}
          value={libraryId}
          onChange={(e) => {
            setLibraryId(e.target.value);
            onChange(null);
          }}
        >
          <option value="">{t("family.rule.chooseLibrary")}</option>
          {libraries.map((l) => (
            <option key={l.id} value={l.id}>
              {l.name}
            </option>
          ))}
        </Select>
      )}
      {needsQuery(kind) && (
        <Input
          type="search"
          label={t("family.rule.search")}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            onChange(null);
          }}
        />
      )}
      {!waitsForLibrary && !waitsForQuery && (
        <Select
          label={t("family.rule.subject")}
          value={value?.kind === kind ? value.id : ""}
          disabled={found === null}
          onChange={(e) => {
            const hit = sorted.find((o) => o.id === e.target.value);
            onChange(hit ? { kind, id: hit.id, name: hit.name } : null);
          }}
        >
          <option value="">
            {found === null
              ? t("common.loading")
              : sorted.length === 0
                ? t("family.rule.nothing")
                : t("family.rule.choose")}
          </option>
          {sorted.map((o) => (
            <option key={o.id} value={o.id}>
              {o.name || o.id}
            </option>
          ))}
        </Select>
      )}
    </div>
  );
}

// ---- a rule's name ----

const names = new Map<string, Promise<string>>();

async function readName(kind: SubjectKind, id: string, locale: string): Promise<string> {
  switch (kind) {
    case "library":
    case "series":
    case "channel":
    case "plugin":
      return (
        (await loadOptions(kind, { libraryId: "", query: "", locale })).find((o) => o.id === id)
          ?.name ?? ""
      );
    case "collection":
      return str(obj(await api.get<unknown>(`/api/collections/${enc(id)}`)).name);
    case "video": {
      const v = obj(await api.get<unknown>(`/api/videos/${enc(id)}`));
      return videoTitle({ title: str(v.title), filename: str(v.filename) });
    }
    case "youtube_source":
      // no route reads one source by its id alone
      return "";
  }
}

/** The name of what a rule names, in this person's view; "" when it cannot be read. */
export function subjectName(kind: SubjectKind, id: string, locale: string): Promise<string> {
  // the lists are shared by every rule of a kind; one read serves them all
  const listed = kind === "library" || kind === "series" || kind === "channel" || kind === "plugin";
  const key = listed ? `${kind}\n${locale}\n${id}` : `${kind}\n${id}`;
  let p = names.get(key);
  if (!p) {
    p = readName(kind, id, locale).catch(() => "");
    names.set(key, p);
  }
  return p;
}

/** Forgets every name read (tests; a rule added or removed elsewhere). */
export function forgetSubjectNames(): void {
  names.clear();
}

/** The names of these subjects, filled in as the home answers. */
export function useSubjectNames(
  subjects: ReadonlyArray<{ subjectKind: SubjectKind; subjectId: string }>
): Record<string, string> {
  const { locale } = useI18n();
  const [got, setGot] = useState<Record<string, string>>({});
  const wanted = subjects.map((s) => `${s.subjectKind}:${s.subjectId}`).join("\n");
  useEffect(() => {
    let alive = true;
    for (const s of subjects) {
      void subjectName(s.subjectKind, s.subjectId, locale).then((name) => {
        if (alive && name) setGot((g) => ({ ...g, [`${s.subjectKind}:${s.subjectId}`]: name }));
      });
    }
    return () => {
      alive = false;
    };
    // `wanted` names the subjects: the array itself is new on every render
  }, [wanted, locale]);
  return got;
}

// ---- how long ----

export const EXPIRY_CHOICES = ["always", "today", "week"] as const;
export type ExpiryChoice = (typeof EXPIRY_CHOICES)[number];

/** The instant a choice ends at: today's end on this device, seven days on; null: never. */
export function expiryOf(choice: ExpiryChoice, now = new Date()): string | null {
  if (choice === "always") return null;
  if (choice === "today") {
    const end = new Date(now);
    end.setHours(23, 59, 59, 0);
    return end.toISOString();
  }
  return new Date(now.getTime() + 7 * 24 * 60 * 60 * 1000).toISOString();
}

export function ExpirySelect({
  value,
  onChange,
  label,
}: {
  value: ExpiryChoice;
  onChange: (next: ExpiryChoice) => void;
  label?: string;
}) {
  const { t } = useI18n();
  return (
    <Select
      label={label ?? t("family.expiry.label")}
      value={value}
      onChange={(e) => onChange(e.target.value as ExpiryChoice)}
    >
      {EXPIRY_CHOICES.map((c) => (
        <option key={c} value={c}>
          {t(`family.expiry.${c}`)}
        </option>
      ))}
    </Select>
  );
}
