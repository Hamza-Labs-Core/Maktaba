// Settings → Rules. The rules detection, grouping and channel planning
// follow, edited from the schema the API serves (GET /api/rules): one tab
// per section, fields grouped under their group heading, an editor per
// field kind. Only what the admin edits is sent (PATCH
// /api/rules/{section} {set, reset}); everything else keeps the built-in
// default. Non-admins see the values read-only.
//
// Edits are kept per section until saved or discarded. A save (or a
// "Back to default") clears only the edits it carried that are still
// unchanged, so edits made while it was under way — or in another tab —
// stay.
import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { Badge } from "@ds/components/Badge/Badge";
import { Button } from "@ds/components/Button/Button";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { PlanPreviewPanel } from "../../components/queue/PlanPreviewPanel";
import { useAuth } from "../../lib/auth";
import { lookupMessage, useI18n, type Locale } from "../../lib/i18n";
import { rulesApi, rulesError, type RuleField, type RuleSection } from "../../lib/rulesApi";
import { Editor, tableProblems, type T } from "./RuleEditors";
import { controlOf, same, subGroups, valueProblem, weekdayNames, WEEKDAYS } from "./ruleKinds";
import { StripRulesTools } from "./StripRulesTools";
import { RemoteUsage } from "../Admin/RemoteUsage";
import { HelpLink } from "../../help/HelpLink";
import { ruleHelp } from "../../help/registry";
import "./rules.css";
import { LinesPlaceholder, Loading } from "../../components/Placeholders";

/**
 * The rules schema in the reader's language: the catalog keys
 * `rules.<section>.title` / `.what`, `rules.<section>.<field>.label` /
 * `.help` and `rules.group.<group>` replace the schema's English where they
 * exist (names, values and choices stay as they are).
 */
export function localizeSections(sections: RuleSection[], locale: Locale): RuleSection[] {
  const tr = (key: string, fallback: string) => lookupMessage(locale, key) ?? fallback;
  return sections.map((s) => ({
    ...s,
    title: tr(`rules.${s.key}.title`, s.title),
    what: tr(`rules.${s.key}.what`, s.what),
    fields: s.fields.map((f) => ({
      ...f,
      label: tr(`rules.${s.key}.${f.name}.label`, f.label),
      help: tr(`rules.${s.key}.${f.name}.help`, f.help),
      group: tr(`rules.group.${f.group}`, f.group),
    })),
  }));
}

type Edits = Record<string, Record<string, unknown>>;
type Errors = Record<string, Record<string, string>>;

/** What is wrong with a value the admin typed, as a message (null: nothing). */
function problemText(f: RuleField, v: unknown, t: T): string | null {
  if (controlOf(f) === "table") {
    const { cols, cells } = tableProblems(f, v, t);
    const first = Object.entries(cells)[0];
    if (!first) return null;
    const [i, j] = first[0].split(":").map(Number);
    return t("settings.rules.err.cell", {
      row: i + 1,
      col: cols[j]?.label ?? "",
      message: t(first[1].key, first[1].vars),
    });
  }
  const p = valueProblem(f, v);
  return p ? t(p.key, p.vars) : null;
}

function show(v: unknown, t: T): string {
  if (v === null || v === undefined || v === "") return t("settings.rules.empty");
  if (Array.isArray(v)) {
    if (!v.length) return t("settings.rules.none");
    return v
      .map((x) =>
        Array.isArray(x)
          ? x.join(" – ")
          : x && typeof x === "object"
            ? String(
                (x as Record<string, unknown>).key ??
                  (x as Record<string, unknown>).name ??
                  JSON.stringify(x)
              )
            : String(x)
      )
      .join(", ");
  }
  if (typeof v === "object") return Object.keys(v as object).join(", ");
  if (typeof v === "boolean") return v ? t("settings.rules.on") : t("settings.rules.off");
  return String(v);
}

interface RowProps {
  f: RuleField;
  sectionKey: string;
  value: unknown;
  dirty: boolean;
  /** the server's message for this setting (a failed save) */
  error?: string;
  /** what is wrong with the typed value (blocks the save) */
  problem?: string | null;
  canEdit: boolean;
  busy: boolean;
  onChange: (v: unknown) => void;
  onReset: () => void;
  onUndo: () => void;
}

function RuleRow({
  f,
  sectionKey,
  value,
  dirty,
  error,
  problem,
  canEdit,
  busy,
  onChange,
  onReset,
  onUndo,
}: RowProps) {
  const { t, locale } = useI18n();
  const labelId = `rule-${sectionKey}-${f.name}-label`;
  const helpId = `rule-${sectionKey}-${f.name}-help`;
  const errId = `rule-${sectionKey}-${f.name}-err`;
  const control = controlOf(f);
  const wide = control === "table";
  const cls = ["mkt-rules__set"];
  if (wide) cls.push("mkt-rules__set--wide");
  if (f.changed) cls.push("mkt-rules__set--changed");
  if (dirty) cls.push("mkt-rules__set--dirty");
  if (problem || error) cls.push("mkt-rules__set--bad");
  const message =
    error ?? problem ?? (f.invalid ? t("settings.rules.invalid", { reason: f.invalid }) : null);
  const describedBy = [f.help ? helpId : null, message ? errId : null].filter(Boolean).join(" ");
  const names = weekdayNames(locale);
  const dayName = (d: unknown) => {
    const i = (WEEKDAYS as readonly unknown[]).indexOf(d);
    return i >= 0 ? names[i].long : String(d);
  };
  const defaultText =
    control === "days" && Array.isArray(f.default) && f.default.length
      ? f.default.map(dayName).join(locale === "ar" ? "، " : ", ")
      : control === "weekday"
        ? dayName(f.default)
        : show(f.default, t);
  return (
    <div className={cls.join(" ")} role="group" aria-labelledby={labelId} data-rule={f.name}>
      <div className="mkt-rules__about">
        <div className="mkt-rules__name">
          <span id={labelId}>{f.label}</span>
          <code>{f.name}</code>
        </div>
        {f.help && (
          <p className="mkt-rules__help" id={helpId} dir="auto">
            {f.help}
          </p>
        )}
        <p className="mkt-rules__default">
          <span dir="auto">{t("settings.rules.default", { value: defaultText })}</span>
          {f.changed && (
            <>
              {" · "}
              <strong className="mkt-rules__changed">{t("settings.rules.changed")}</strong>
            </>
          )}
          {dirty && (
            <>
              {" · "}
              <strong className="mkt-rules__unsaved">{t("settings.rules.unsavedMark")}</strong>
            </>
          )}
          {" · "}
          <HelpLink
            to={ruleHelp(sectionKey)}
            className="mkt-rules__learn"
            label={t("help.ruleHelp", { setting: f.label })}
          />
        </p>
      </div>
      <div className="mkt-rules__editor">
        <Editor
          f={f}
          value={value}
          labelId={labelId}
          describedBy={describedBy || undefined}
          invalid={!!(problem || error)}
          readOnly={!canEdit}
          onChange={onChange}
          serverError={error}
          show={(v) => show(v, t)}
        />
        {message && (
          <p className="mkt-rules__error" id={errId} role={error || problem ? "alert" : undefined}>
            {message}
          </p>
        )}
        {canEdit && (dirty || f.changed || f.invalid) && (
          <div className="mkt-rules__actions">
            {dirty && (
              // unsaved over the default: dropping the edit is the default
              <Button size="sm" variant="ghost" aria-describedby={labelId} onClick={onUndo}>
                {f.changed || f.invalid ? t("settings.rules.undo") : t("settings.rules.reset")}
              </Button>
            )}
            {(f.changed || f.invalid) && (
              <Button
                size="sm"
                variant="ghost"
                disabled={busy}
                aria-describedby={labelId}
                onClick={onReset}
              >
                {t("settings.rules.reset")}
              </Button>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

export function RulesSection() {
  const { t, locale } = useI18n();
  const { user } = useAuth();
  const canEdit = !!user?.is_admin;
  const [rawSections, setSections] = useState<RuleSection[] | null>(null);
  // the schema is English; a section, group or field with a catalog entry
  // shows it in the reader's language
  const sections = useMemo(
    () => (rawSections ? localizeSections(rawSections, locale) : null),
    [rawSections, locale]
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  // a link may open one section, found by a field's name (?section=…&q=…: a switch of
  // the home's checklist does)
  const [params] = useSearchParams();
  const wanted = params.get("section") ?? "";
  const [tab, setTab] = useState("");
  const [edits, setEdits] = useState<Edits>({});
  const [errors, setErrors] = useState<Errors>({});
  const [query, setQuery] = useState(() => params.get("q") ?? "");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ ok: boolean; text: string; link?: string } | null>(null);
  // bumped on Discard so every editor drops its typed text
  const [epoch, setEpoch] = useState(0);
  // the groups and sub-groups folded away ("section/group[/sub]")
  const [folded, setFolded] = useState<Set<string>>(() => new Set());
  const toggleFold = (id: string) =>
    setFolded((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  const load = useCallback(async () => {
    const r = await rulesApi.list();
    // a fresh array: the localised copy is rebuilt from what was read.
    // sections or a section's fields null / missing read as empty lists.
    const read: RuleSection[] = (Array.isArray(r?.sections) ? r.sections : []).map((s) => ({
      ...s,
      fields: Array.isArray(s.fields) ? s.fields : [],
    }));
    setSections(read);
    setLoadError(null);
    setTab((cur) =>
      read.some((s) => s.key === cur)
        ? cur
        : (read.find((s) => s.key === wanted)?.key ?? read[0]?.key ?? "")
    );
    // an edit that now equals the saved value is no longer an edit
    setEdits((prev) => {
      const next: Edits = {};
      for (const s of read) {
        const e = prev[s.key];
        if (!e) continue;
        const kept: Record<string, unknown> = {};
        for (const f of s.fields) {
          if (f.name in e && !same(e[f.name], f.value)) kept[f.name] = e[f.name];
        }
        next[s.key] = kept;
      }
      return next;
    });
  }, [wanted]);

  useEffect(() => {
    load().catch((e) => setLoadError(rulesError(e).message));
  }, [load]);

  const setEdit = (sectionKey: string, f: RuleField, v: unknown) => {
    setEdits((prev) => {
      const cur = { ...(prev[sectionKey] ?? {}) };
      if (same(v, f.value)) delete cur[f.name];
      else cur[f.name] = v;
      return { ...prev, [sectionKey]: cur };
    });
    setErrors((prev) => {
      if (!prev[sectionKey]?.[f.name]) return prev;
      const cur = { ...prev[sectionKey] };
      delete cur[f.name];
      return { ...prev, [sectionKey]: cur };
    });
  };

  // Save the section's edits, or (with `reset`) put those names back to
  // their defaults. What the request carries is captured now.
  async function send(sectionKey: string, reset?: string[]) {
    const pending = edits[sectionKey] ?? {};
    const set = reset ? {} : { ...pending };
    const carried: Record<string, unknown> = reset
      ? Object.fromEntries(reset.filter((n) => n in pending).map((n) => [n, pending[n]]))
      : set;
    const names = reset ?? Object.keys(set);
    setBusy(true);
    setNotice(null);
    try {
      let changed = 0;
      let draft: number | null = null;
      let draftId: string | null = null;
      try {
        const r = await rulesApi.patch(sectionKey, set, reset ?? []);
        changed = r?.changed?.length ?? 0;
        draft = r?.draft_change?.number ?? null;
        draftId = r?.draft_change?.id ?? null;
      } catch (e) {
        const { message, field } = rulesError(e);
        if (field) {
          setErrors((prev) => ({
            ...prev,
            [sectionKey]: { ...prev[sectionKey], [field]: message },
          }));
        }
        setNotice({ ok: false, text: t("settings.rules.notSaved", { message }) });
        return;
      }
      setEdits((prev) => {
        const cur = { ...(prev[sectionKey] ?? {}) };
        for (const [n, v] of Object.entries(carried)) {
          if (n in cur && same(cur[n], v)) delete cur[n];
        }
        return { ...prev, [sectionKey]: cur };
      });
      setErrors((prev) => {
        const cur = { ...(prev[sectionKey] ?? {}) };
        for (const n of names) delete cur[n];
        return { ...prev, [sectionKey]: cur };
      });
      setNotice({
        ok: true,
        text:
          draft !== null
            ? t("settings.rules.savedToDraft", { n: draft })
            : reset
              ? t("settings.rules.resetDone")
              : t("settings.rules.saved", { n: changed }),
        // the Changes page of the draft (a plain link: this section also renders outside the router)
        link: draftId ? `/admin/live-tv/changes/${draftId}` : undefined,
      });
      try {
        await load();
      } catch (e) {
        setLoadError(rulesError(e).message);
      }
    } finally {
      setBusy(false);
    }
  }

  const discard = (sectionKey: string) => {
    setEdits((prev) => ({ ...prev, [sectionKey]: {} }));
    setErrors((prev) => ({ ...prev, [sectionKey]: {} }));
    setNotice(null);
    setEpoch((n) => n + 1);
  };

  if (!sections) {
    return (
      <div className="mkt-settings__panel mkt-rules">
        <h2>{t("settings.rules.title")}</h2>
        {loadError ? (
          <p role="alert" className="mkt-rules__banner mkt-rules__banner--bad">
            {t("settings.rules.loadError")} {loadError}
          </p>
        ) : (
          <Loading loading>
            <LinesPlaceholder lines={8} />
          </Loading>
        )}
      </div>
    );
  }

  const tabLabel = (s: RuleSection): ReactNode => {
    const changed = s.fields.filter((f) => f.changed).length;
    const unsaved = Object.keys(edits[s.key] ?? {}).length;
    const parts: string[] = [];
    if (changed || unsaved) parts.push(t("settings.rules.count.changed", { n: changed }));
    if (unsaved) parts.push(t("settings.rules.count.unsaved", { n: unsaved }));
    return (
      <span title={s.what}>
        {s.title}
        {parts.length > 0 && <span className="mkt-rules__tabcount">{parts.join(" · ")}</span>}
      </span>
    );
  };

  const body = (s: RuleSection) => {
    const pending = edits[s.key] ?? {};
    const errs = errors[s.key] ?? {};
    const unsaved = Object.keys(pending).length;
    const needle = query.trim().toLowerCase();
    const fields = s.fields.filter(
      (f) => !needle || [f.name, f.label, f.help, f.group].join(" ").toLowerCase().includes(needle)
    );
    const groups = [...new Set(fields.map((f) => f.group))];
    // what is wrong with the typed values: shown at each field, blocks the save
    const problems: Record<string, string> = {};
    for (const f of s.fields) {
      if (!(f.name in pending)) continue;
      const p = problemText(f, pending[f.name], t);
      if (p) problems[f.name] = p;
    }
    const nProblems = Object.keys(problems).length;
    const searching = needle !== "";
    const foldIds = groups.flatMap((g) => [
      `${s.key}/${g}`,
      ...subGroups(s.fields.filter((f) => f.group === g))
        .filter((sg) => sg.title)
        .map((sg) => `${s.key}/${g}/${sg.title}`),
    ]);
    const allFolded = foldIds.length > 0 && foldIds.every((id) => folded.has(id));
    const row = (f: RuleField) => (
      <RuleRow
        key={`${f.name}:${epoch}`}
        f={f}
        sectionKey={s.key}
        value={f.name in pending ? pending[f.name] : f.value}
        dirty={f.name in pending}
        error={errs[f.name]}
        problem={problems[f.name]}
        canEdit={canEdit}
        busy={busy}
        onChange={(v) => setEdit(s.key, f, v)}
        onReset={() => void send(s.key, [f.name])}
        onUndo={() => setEdit(s.key, f, f.value)}
      />
    );
    const counts = (fs: RuleField[]) => {
      const changed = fs.filter((f) => f.changed).length;
      const dirty = fs.filter((f) => f.name in pending).length;
      const bad = fs.filter((f) => problems[f.name] || errs[f.name]).length;
      return (
        <span className="mkt-rules__groupcounts">
          <Badge>{t("settings.rules.groupCount", { n: fs.length })}</Badge>
          {changed > 0 && (
            <Badge tone="warn">{t("settings.rules.count.changed", { n: changed })}</Badge>
          )}
          {dirty > 0 && (
            <Badge tone="accent">{t("settings.rules.count.unsaved", { n: dirty })}</Badge>
          )}
          {bad > 0 && <Badge tone="error">{t("settings.rules.count.errors", { n: bad })}</Badge>}
        </span>
      );
    };
    // a heading whose button folds the block away (WAI disclosure pattern)
    const fold = (
      id: string,
      domId: string,
      title: string,
      fs: RuleField[],
      level: 3 | 4,
      content: ReactNode
    ) => {
      const open = searching || !folded.has(id);
      const H = level === 3 ? "h3" : "h4";
      return (
        <section
          key={id}
          className={level === 3 ? "mkt-rules__group" : "mkt-rules__sub"}
          aria-labelledby={`${domId}-h`}
        >
          <div className="mkt-rules__grouphead">
            <H id={`${domId}-h`} className="mkt-rules__grouptitle">
              <button
                type="button"
                className="mkt-rules__fold"
                aria-expanded={open}
                aria-controls={domId}
                disabled={searching}
                onClick={() => toggleFold(id)}
              >
                <span className="mkt-rules__chev" aria-hidden="true" />
                {title}
              </button>
            </H>
            {counts(fs)}
          </div>
          <div id={domId} className="mkt-rules__groupbody" hidden={!open}>
            {content}
          </div>
        </section>
      );
    };
    const invalid = s.fields.filter((f) => f.invalid).length;
    const unknown = s.unknown ?? [];
    return (
      <div className="mkt-rules__section">
        <p className="mkt-muted">{s.what}</p>
        <div className="mkt-rules__bar">
          <input
            type="search"
            className="mkt-rules__input mkt-rules__search"
            dir="auto"
            aria-label={t("settings.rules.search")}
            placeholder={t("settings.rules.search")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          {canEdit && (
            <>
              <span className="mkt-muted" aria-live="polite">
                {unsaved ? t("settings.rules.count.unsaved", { n: unsaved }) : ""}
              </span>
              <Button variant="ghost" disabled={!unsaved || busy} onClick={() => discard(s.key)}>
                {t("settings.rules.discard")}
              </Button>
              <Button
                disabled={!unsaved || nProblems > 0}
                loading={busy}
                aria-describedby={nProblems ? `rules-${s.key}-fix` : undefined}
                onClick={() => void send(s.key)}
              >
                {t("settings.rules.save")}
              </Button>
            </>
          )}
          {foldIds.length > 1 && !searching && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                setFolded((prev) => {
                  const next = new Set(prev);
                  for (const id of foldIds) {
                    if (allFolded) next.delete(id);
                    else next.add(id);
                  }
                  return next;
                })
              }
            >
              {allFolded ? t("settings.rules.expandAll") : t("settings.rules.collapseAll")}
            </Button>
          )}
        </div>
        {nProblems > 0 && (
          <p id={`rules-${s.key}-fix`} className="mkt-rules__banner mkt-rules__banner--bad">
            {t("settings.rules.fixErrors", { n: nProblems })}
          </p>
        )}
        {notice && (
          <p
            role={notice.ok ? "status" : "alert"}
            className={notice.ok ? "mkt-rules__notice" : "mkt-rules__banner mkt-rules__banner--bad"}
          >
            {notice.text}
            {notice.link && (
              <>
                {" "}
                <a href={notice.link} data-testid="rules-draft-link">
                  {t("settings.rules.openDraft")}
                </a>
              </>
            )}
          </p>
        )}
        {invalid > 0 && (
          <p className="mkt-rules__banner mkt-rules__banner--warn">
            {t("settings.rules.invalidBanner", { n: invalid })}
          </p>
        )}
        {unknown.length > 0 && (
          <div className="mkt-rules__banner mkt-rules__banner--warn">
            <span>{t("settings.rules.unknown", { names: unknown.join(", ") })}</span>
            {canEdit && (
              <Button
                size="sm"
                variant="secondary"
                disabled={busy}
                onClick={() => void send(s.key, unknown)}
              >
                {t("settings.rules.removeUnknown")}
              </Button>
            )}
          </div>
        )}
        {s.key === "tv_strip" && (
          <div className="mkt-rules__preview">
            <StripRulesTools
              fields={s.fields}
              pending={pending}
              readOnly={!canEdit || busy}
              onSet={(f, v) => setEdit(s.key, f, v)}
            />
          </div>
        )}
        {s.key === "tv_remote" && (
          <div className="mkt-q mkt-rules__preview">
            <RemoteUsage admin={canEdit} />
          </div>
        )}
        {s.key === "planner" && (
          <div className="mkt-q mkt-rules__preview">
            <PlanPreviewPanel canRun={canEdit} compact unsaved={unsaved} />
          </div>
        )}
        {s.fields.length === 0 ? (
          <p className="mkt-muted">{t("common.empty")}</p>
        ) : groups.length === 0 ? (
          <p className="mkt-muted">{t("settings.rules.noMatch", { q: query.trim() })}</p>
        ) : (
          groups.map((g, gi) => {
            const inGroup = fields.filter((f) => f.group === g);
            // sub-groups come from the whole group, so a search keeps them
            const subs = subGroups(s.fields.filter((f) => f.group === g))
              .map((sg) => ({ ...sg, fields: sg.fields.filter((f) => inGroup.includes(f)) }))
              .filter((sg) => sg.fields.length);
            return fold(
              `${s.key}/${g}`,
              `rules-${s.key}-g${gi}`,
              g,
              inGroup,
              3,
              subs.map((sg, si) =>
                sg.title === null ? (
                  <div key="own" className="mkt-rules__own">
                    {sg.fields.map(row)}
                  </div>
                ) : (
                  fold(
                    `${s.key}/${g}/${sg.title}`,
                    `rules-${s.key}-g${gi}-s${si}`,
                    sg.title,
                    sg.fields,
                    4,
                    sg.fields.map(row)
                  )
                )
              )
            );
          })
        )}
      </div>
    );
  };

  const active = sections.find((s) => s.key === tab) ?? sections[0];
  return (
    <div className="mkt-settings__panel mkt-rules">
      <h2>{t("settings.rules.title")}</h2>
      <p className="mkt-muted">{t("settings.rules.intro")}</p>
      {!canEdit && <p className="mkt-rules__banner">{t("settings.rules.readOnly")}</p>}
      {loadError && (
        <p role="alert" className="mkt-rules__banner mkt-rules__banner--bad">
          {t("settings.rules.loadError")} {loadError}
        </p>
      )}
      {sections.length === 0 && <p className="mkt-muted">{t("common.empty")}</p>}
      <Tabs
        label={t("settings.rules.tabs")}
        value={active?.key}
        onChange={(id) => {
          setTab(id);
          setNotice(null);
        }}
        items={sections.map((s) => ({ id: s.key, label: tabLabel(s), content: body(s) }))}
      />
    </div>
  );
}
