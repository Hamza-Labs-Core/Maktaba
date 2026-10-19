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
import { useCallback, useEffect, useRef, useState } from "react";
import type { ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { Tabs } from "@ds/components/Tabs/Tabs";
import { PlanPreviewPanel } from "../../components/queue/PlanPreviewPanel";
import { useAuth } from "../../lib/auth";
import { useI18n } from "../../lib/i18n";
import { rulesApi, rulesError, type RuleField, type RuleSection } from "../../lib/rulesApi";
import "./rules.css";

type Edits = Record<string, Record<string, unknown>>;
type Errors = Record<string, Record<string, string>>;
type T = (key: string, vars?: Record<string, string | number>) => string;

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);
const baseKind = (kind: string) => kind.replace("?", "");
const isOptional = (kind: string) => kind.endsWith("?");
const asArray = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

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

// A text draft over a typed value: the input shows what was typed (a
// half-typed "1." or a list's new empty line) for as long as it still
// reads back as the current value, and the value's own text otherwise
// (after a discard, a reset or a reload).
function useDraft<R>(
  value: unknown,
  format: (v: unknown) => R,
  parse: (r: R) => unknown
): [R, (r: R) => void] {
  const [raw, setRaw] = useState<R>(() => format(value));
  const shown = same(parse(raw), value) ? raw : format(value);
  return [shown, setRaw];
}

interface EditorProps {
  f: RuleField;
  value: unknown;
  labelId: string;
  readOnly: boolean;
  onChange: (v: unknown) => void;
}

function NumberEditor({ f, value, labelId, readOnly, onChange }: EditorProps) {
  const opt = isOptional(f.kind);
  const parse = (r: string) => (r.trim() === "" ? (opt ? null : "") : Number(r));
  const [text, setText] = useDraft(
    value,
    (v) => (v === null || v === undefined ? "" : String(v)),
    parse
  );
  return (
    <input
      type="number"
      className="mkt-rules__input mkt-rules__input--short"
      aria-labelledby={labelId}
      step={baseKind(f.kind) === "int" ? 1 : "any"}
      min={f.minimum}
      value={text}
      readOnly={readOnly}
      placeholder={opt ? "—" : undefined}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parse(e.target.value));
      }}
    />
  );
}

// list / words: one item per line. A trailing space is kept (in a phrase
// list it means "the phrase ends here"); empty lines are dropped.
function parseLines(r: string): string[] {
  return r
    .split("\n")
    .map((x) => x.replace(/\r$/, "").replace(/^\s+/, ""))
    .filter((x) => x.trim() !== "");
}

function ListEditor({ value, labelId, readOnly, onChange }: EditorProps) {
  const { t } = useI18n();
  const hintId = `${labelId}-hint`;
  const [text, setText] = useDraft(value, (v) => asArray(v).map(String).join("\n"), parseLines);
  return (
    <>
      <textarea
        className="mkt-rules__input mkt-rules__textarea"
        aria-labelledby={labelId}
        aria-describedby={hintId}
        dir="auto"
        rows={Math.min(10, Math.max(3, asArray(value).length + 1))}
        value={text}
        readOnly={readOnly}
        onChange={(e) => {
          setText(e.target.value);
          onChange(parseLines(e.target.value));
        }}
      />
      <span className="mkt-rules__hint" id={hintId}>
        {t("settings.rules.onePerLine")}
      </span>
    </>
  );
}

interface Column {
  name: string;
  label: string;
  kind: string;
}

function columnsOf(f: RuleField, t: T): Column[] {
  if (baseKind(f.kind) === "map") {
    return [
      { name: "key", label: t("settings.rules.mapKey"), kind: "str" },
      { name: "values", label: t("settings.rules.mapValues"), kind: "list" },
    ];
  }
  const sample = asArray(f.default)[0] ?? asArray(f.value)[0];
  return (f.columns ?? []).map((c, j) => {
    let kind = f.column_kinds?.[j];
    if (!kind) {
      const s = Array.isArray(sample)
        ? sample[j]
        : sample && typeof sample === "object"
          ? (sample as Record<string, unknown>)[c]
          : undefined;
      kind =
        f.time_columns?.includes(j) || /^(start|end|from|until)$/.test(c)
          ? "time"
          : typeof s === "number"
            ? "float?"
            : Array.isArray(s)
              ? "list"
              : "str";
    }
    return { name: c, label: c.replace(/_/g, " "), kind };
  });
}

// A row is an object keyed by column name when the default rows are
// objects, else an array in column order.
function rowsAreObjects(f: RuleField): boolean {
  const sample = asArray(f.default)[0] ?? asArray(f.value)[0];
  return !!sample && typeof sample === "object" && !Array.isArray(sample);
}

function cellText(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.join(", ");
  return String(v);
}

function parseCell(kind: string, s: string): unknown {
  const k = baseKind(kind);
  if (k === "int" || k === "float") return s.trim() === "" ? null : Number(s);
  if (k === "list" || k === "words")
    return s
      .split(",")
      .map((x) => x.trim())
      .filter(Boolean);
  if (k === "str" && isOptional(kind) && s === "") return null;
  return s;
}

function TableEditor({ f, value, labelId, readOnly, onChange }: EditorProps) {
  const { t } = useI18n();
  const isMap = baseKind(f.kind) === "map";
  const cols = columnsOf(f, t);
  const named = rowsAreObjects(f);
  const tbody = useRef<HTMLTableSectionElement>(null);
  const focusNew = useRef(false);

  const format = (v: unknown): string[][] =>
    isMap
      ? Object.entries((v as Record<string, unknown>) ?? {}).map(([k, arr]) => [
          k,
          asArray(arr).join(", "),
        ])
      : asArray(v).map((r) =>
          cols.map((c, j) =>
            cellText(
              Array.isArray(r)
                ? r[j]
                : r && typeof r === "object"
                  ? (r as Record<string, unknown>)[c.name]
                  : undefined
            )
          )
        );
  const parse = (raw: string[][]): unknown => {
    if (isMap) {
      const o: Record<string, string[]> = {};
      for (const [k, vals] of raw) {
        if (k.trim()) o[k.trim()] = parseCell("list", vals) as string[];
      }
      return o;
    }
    return raw.map((cells) => {
      const typed = cells.map((s, j) => parseCell(cols[j].kind, s));
      return named ? Object.fromEntries(cols.map((c, j) => [c.name, typed[j]])) : typed;
    });
  };
  const [rows, setRows] = useDraft(value, format, parse);
  const commit = (next: string[][]) => {
    setRows(next);
    onChange(parse(next));
  };

  useEffect(() => {
    if (!focusNew.current) return;
    focusNew.current = false;
    tbody.current?.lastElementChild?.querySelector("input")?.focus();
  }, [rows.length]);

  return (
    <>
      <div className="mkt-rules__tablewrap">
        <table className="mkt-rules__table" aria-labelledby={labelId}>
          <thead>
            <tr>
              {cols.map((c) => (
                <th key={c.name} scope="col">
                  {c.label}
                </th>
              ))}
              {!readOnly && (
                <th scope="col">
                  <span className="mkt-rules__sr">{t("settings.rules.removeRow")}</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody ref={tbody}>
            {rows.map((cells, i) => (
              <tr key={i}>
                {cells.map((s, j) => {
                  const k = baseKind(cols[j].kind);
                  const numeric = k === "int" || k === "float";
                  return (
                    <td key={cols[j].name}>
                      <input
                        className={`mkt-rules__cell${numeric || k === "time" ? " mkt-rules__cell--short" : ""}`}
                        type={k === "time" ? "time" : numeric ? "number" : "text"}
                        step={numeric ? (k === "int" ? 1 : "any") : undefined}
                        dir={numeric || k === "time" ? undefined : "auto"}
                        aria-label={cols[j].label}
                        value={s}
                        readOnly={readOnly}
                        onChange={(e) =>
                          commit(
                            rows.map((r, ri) =>
                              ri === i ? r.map((c, ci) => (ci === j ? e.target.value : c)) : r
                            )
                          )
                        }
                      />
                    </td>
                  );
                })}
                {!readOnly && (
                  <td className="mkt-rules__cell-x">
                    <Button
                      size="sm"
                      variant="ghost"
                      aria-label={t("settings.rules.removeRow")}
                      onClick={() => commit(rows.filter((_, ri) => ri !== i))}
                    >
                      <span aria-hidden="true">✕</span>
                    </Button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {!readOnly && (
        <div>
          <Button
            size="sm"
            variant="secondary"
            aria-describedby={labelId}
            onClick={() => {
              focusNew.current = true;
              commit([...rows, cols.map(() => "")]);
            }}
          >
            {t("settings.rules.addRow")}
          </Button>
        </div>
      )}
    </>
  );
}

function Editor(props: EditorProps) {
  const { t } = useI18n();
  const { f, value, labelId, readOnly, onChange } = props;
  const k = baseKind(f.kind);
  const opt = isOptional(f.kind);
  if (k === "bool") {
    return (
      <label className="mkt-rules__switch">
        <input
          type="checkbox"
          aria-labelledby={labelId}
          checked={value === true}
          disabled={readOnly}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span aria-hidden="true">{value ? t("settings.rules.on") : t("settings.rules.off")}</span>
      </label>
    );
  }
  if (f.choices) {
    const current = String(value ?? "");
    const options = f.choices.includes(current) ? f.choices : [current, ...f.choices];
    return (
      <select
        className="mkt-rules__input mkt-rules__input--short"
        aria-labelledby={labelId}
        value={current}
        disabled={readOnly}
        onChange={(e) => onChange(e.target.value)}
      >
        {options.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
    );
  }
  if (k === "int" || k === "float") return <NumberEditor {...props} />;
  if (k === "time") {
    return (
      <input
        type="time"
        className="mkt-rules__input mkt-rules__input--short"
        aria-labelledby={labelId}
        value={String(value ?? "")}
        readOnly={readOnly}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  if (k === "str") {
    return (
      <input
        type="text"
        className="mkt-rules__input"
        aria-labelledby={labelId}
        dir="auto"
        value={String(value ?? "")}
        readOnly={readOnly}
        placeholder={opt ? t("settings.rules.empty") : undefined}
        onChange={(e) => onChange(opt && e.target.value === "" ? null : e.target.value)}
      />
    );
  }
  if (k === "list" || k === "words") return <ListEditor {...props} />;
  if (k === "map" || k === "rows") return <TableEditor {...props} />;
  return (
    <p className="mkt-rules__hint">
      {t("settings.rules.notEditable")} <span dir="auto">{show(value, t)}</span>
    </p>
  );
}

interface RowProps {
  f: RuleField;
  sectionKey: string;
  value: unknown;
  dirty: boolean;
  error?: string;
  canEdit: boolean;
  busy: boolean;
  onChange: (v: unknown) => void;
  onReset: () => void;
}

function RuleRow({
  f,
  sectionKey,
  value,
  dirty,
  error,
  canEdit,
  busy,
  onChange,
  onReset,
}: RowProps) {
  const { t } = useI18n();
  const labelId = `rule-${sectionKey}-${f.name}-label`;
  const wide = /rows|map/.test(f.kind);
  const cls = ["mkt-rules__set"];
  if (wide) cls.push("mkt-rules__set--wide");
  if (f.changed) cls.push("mkt-rules__set--changed");
  if (dirty) cls.push("mkt-rules__set--dirty");
  return (
    <div className={cls.join(" ")} role="group" aria-labelledby={labelId} data-rule={f.name}>
      <div className="mkt-rules__about">
        <div className="mkt-rules__name">
          <span id={labelId}>{f.label}</span>
          <code>{f.name}</code>
        </div>
        {f.help && (
          <p className="mkt-rules__help" dir="auto">
            {f.help}
          </p>
        )}
        <p className="mkt-rules__default">
          <span dir="auto">{t("settings.rules.default", { value: show(f.default, t) })}</span>
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
        </p>
      </div>
      <div className="mkt-rules__editor">
        <Editor f={f} value={value} labelId={labelId} readOnly={!canEdit} onChange={onChange} />
        {(error || f.invalid) && (
          <p className="mkt-rules__error" role={error ? "alert" : undefined}>
            {error ?? t("settings.rules.invalid", { reason: f.invalid ?? "" })}
          </p>
        )}
        {canEdit && (f.changed || f.invalid) && (
          <div>
            <Button
              size="sm"
              variant="ghost"
              disabled={busy}
              aria-describedby={labelId}
              onClick={onReset}
            >
              {t("settings.rules.reset")}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}

export function RulesSection() {
  const { t } = useI18n();
  const { user } = useAuth();
  const canEdit = !!user?.is_admin;
  const [sections, setSections] = useState<RuleSection[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [tab, setTab] = useState("");
  const [edits, setEdits] = useState<Edits>({});
  const [errors, setErrors] = useState<Errors>({});
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<{ ok: boolean; text: string } | null>(null);
  // bumped on Discard so every editor drops its typed text
  const [epoch, setEpoch] = useState(0);

  const load = useCallback(async () => {
    const r = await rulesApi.list();
    setSections(r.sections);
    setLoadError(null);
    setTab((cur) => (r.sections.some((s) => s.key === cur) ? cur : (r.sections[0]?.key ?? "")));
    // an edit that now equals the saved value is no longer an edit
    setEdits((prev) => {
      const next: Edits = {};
      for (const s of r.sections) {
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
  }, []);

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
      try {
        const r = await rulesApi.patch(sectionKey, set, reset ?? []);
        changed = r?.changed?.length ?? 0;
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
        text: reset ? t("settings.rules.resetDone") : t("settings.rules.saved", { n: changed }),
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
          <p className="mkt-loading" role="status">
            {t("common.loading")}
          </p>
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
              <Button disabled={!unsaved} loading={busy} onClick={() => void send(s.key)}>
                {t("settings.rules.save")}
              </Button>
            </>
          )}
        </div>
        {notice && (
          <p
            role={notice.ok ? "status" : "alert"}
            className={notice.ok ? "mkt-rules__notice" : "mkt-rules__banner mkt-rules__banner--bad"}
          >
            {notice.text}
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
        {s.key === "planner" && (
          <div className="mkt-q mkt-rules__preview">
            <PlanPreviewPanel canRun={canEdit} compact unsaved={unsaved} />
          </div>
        )}
        {groups.length === 0 ? (
          <p className="mkt-muted">{t("settings.rules.noMatch", { q: query.trim() })}</p>
        ) : (
          groups.map((g, gi) => {
            const headingId = `rules-${s.key}-g${gi}`;
            return (
              <section key={g} className="mkt-rules__group" aria-labelledby={headingId}>
                <h3 id={headingId}>{g}</h3>
                {fields
                  .filter((f) => f.group === g)
                  .map((f) => (
                    <RuleRow
                      key={`${f.name}:${epoch}`}
                      f={f}
                      sectionKey={s.key}
                      value={f.name in pending ? pending[f.name] : f.value}
                      dirty={f.name in pending}
                      error={errs[f.name]}
                      canEdit={canEdit}
                      busy={busy}
                      onChange={(v) => setEdit(s.key, f, v)}
                      onReset={() => void send(s.key, [f.name])}
                    />
                  ))}
              </section>
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
