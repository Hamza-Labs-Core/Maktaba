// The editor of each setting kind on Settings → Rules, chosen from the
// setting's descriptor (ruleKinds.controlOf): a switch, a select, day
// chips, a time picker, a number with its unit (and a slider when its
// range is known), a tag editor, one-per-line text, or a table whose
// cells use the same controls.
import { useEffect, useRef, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Chip } from "@ds/components/Chip/Chip";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useI18n } from "../../lib/i18n";
import type { RuleField } from "../../lib/rulesApi";
import {
  WEEKDAYS,
  asArray,
  baseKind,
  cellProblems,
  columnControl,
  controlOf,
  friendlyDuration,
  isOptional,
  same,
  sliderRange,
  unitOf,
  weekdayNames,
  wrapsMidnight,
  type Column,
  type Problem,
  type Unit,
} from "./ruleKinds";

export type T = (key: string, vars?: Record<string, string | number>) => string;

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

export interface EditorProps {
  f: RuleField;
  value: unknown;
  labelId: string;
  /** ids of the help / error texts the control is described by */
  describedBy?: string;
  invalid?: boolean;
  readOnly: boolean;
  onChange: (v: unknown) => void;
}

function UnitHint({ unit, value }: { unit: Unit | null; value: unknown }) {
  const { t, n } = useI18n();
  if (!unit) return null;
  const friendly = friendlyDuration(value, unit);
  return (
    <>
      <span className="mkt-rules__unit">{t(`settings.rules.unit.${unit}`)}</span>
      {friendly && (
        <span className="mkt-rules__hint">
          {t("settings.rules.approx", {
            value: `${n(friendly.n)} ${t(`settings.rules.unit.${friendly.unit}`)}`,
          })}
        </span>
      )}
    </>
  );
}

function NumberEditor({
  f,
  value,
  labelId,
  describedBy,
  invalid,
  readOnly,
  onChange,
}: EditorProps) {
  const opt = isOptional(f.kind);
  const parse = (r: string) => (r.trim() === "" ? (opt ? null : "") : Number(r));
  const [text, setText] = useDraft(
    value,
    (v) => (v === null || v === undefined ? "" : String(v)),
    parse
  );
  const range = sliderRange(f, value);
  const num = typeof value === "number" ? value : (range?.min ?? 0);
  const unit = unitOf(f.name);
  const set = (raw: string) => {
    setText(raw);
    onChange(parse(raw));
  };
  return (
    <div className="mkt-rules__num">
      {range && (
        <input
          type="range"
          className="mkt-rules__slider"
          aria-labelledby={labelId}
          min={range.min}
          max={range.max}
          step={range.step}
          value={Math.min(range.max, Math.max(range.min, num))}
          disabled={readOnly}
          onChange={(e) => set(e.target.value)}
        />
      )}
      <input
        type="number"
        className="mkt-rules__input mkt-rules__input--num"
        aria-labelledby={labelId}
        aria-describedby={describedBy}
        aria-invalid={invalid || undefined}
        step={baseKind(f.kind) === "int" ? 1 : "any"}
        min={f.minimum}
        max={f.maximum}
        value={text}
        readOnly={readOnly}
        placeholder={opt ? "—" : undefined}
        onChange={(e) => set(e.target.value)}
      />
      <UnitHint unit={unit} value={value} />
    </div>
  );
}

/** HH:MM through a time picker (24:00, which a picker cannot show, as text). */
function TimeInput({
  value,
  className,
  onChange,
  ...rest
}: {
  value: string;
  className: string;
  onChange: (v: string) => void;
  readOnly: boolean;
  "aria-labelledby"?: string;
  "aria-label"?: string;
  "aria-describedby"?: string;
  "aria-invalid"?: boolean;
}) {
  const asText = value === "24:00";
  return (
    <input
      {...rest}
      type={asText ? "text" : "time"}
      inputMode={asText ? "numeric" : undefined}
      className={className}
      dir="ltr"
      value={value}
      onChange={(e) => onChange(e.target.value)}
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

function LinesEditor({ value, labelId, describedBy, readOnly, onChange }: EditorProps) {
  const { t } = useI18n();
  const hintId = `${labelId}-hint`;
  const [text, setText] = useDraft(value, (v) => asArray(v).map(String).join("\n"), parseLines);
  return (
    <>
      <textarea
        className="mkt-rules__input mkt-rules__textarea"
        aria-labelledby={labelId}
        aria-describedby={[hintId, describedBy].filter(Boolean).join(" ")}
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

const splitItems = (s: string) =>
  s
    .split(/[,،\n]/)
    .map((x) => x.trim())
    .filter(Boolean);

/** A list of words or names as removable chips, with an input to add more. */
function ChipsEditor({ value, labelId, describedBy, readOnly, onChange }: EditorProps) {
  const { t } = useI18n();
  const items = asArray(value).map(String);
  const [text, setText] = useState("");
  const hintId = `${labelId}-hint`;
  const add = (raw: string) => {
    const fresh = splitItems(raw).filter((x, i, a) => !items.includes(x) && a.indexOf(x) === i);
    setText("");
    if (fresh.length) onChange([...items, ...fresh]);
  };
  return (
    <div className="mkt-rules__chips">
      {items.length > 0 ? (
        <ul className="mkt-rules__chiplist" aria-labelledby={labelId}>
          {items.map((x, i) => (
            <li key={`${x}-${i}`}>
              <Chip
                onRemove={readOnly ? undefined : () => onChange(items.filter((_, j) => j !== i))}
                removeLabel={t("settings.rules.removeItem", { item: x })}
              >
                <bdi>{x}</bdi>
              </Chip>
            </li>
          ))}
        </ul>
      ) : (
        <span className="mkt-rules__hint">{t("settings.rules.none")}</span>
      )}
      {!readOnly && (
        <>
          <input
            type="text"
            className="mkt-rules__input mkt-rules__chipinput"
            aria-labelledby={labelId}
            aria-describedby={[hintId, describedBy].filter(Boolean).join(" ")}
            dir="auto"
            placeholder={t("settings.rules.addItem")}
            value={text}
            onChange={(e) => {
              const v = e.target.value;
              if (/[,،\n]/.test(v)) add(v);
              else setText(v);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                add(text);
              } else if (e.key === "Backspace" && text === "" && items.length) {
                onChange(items.slice(0, -1));
              }
            }}
            onBlur={() => add(text)}
          />
          <span className="mkt-rules__hint" id={hintId}>
            {t("settings.rules.chipsHint")}
          </span>
        </>
      )}
    </div>
  );
}

/** Weekdays as toggle buttons (Mon … Sun in the locale's names). */
function DayChips({
  value,
  readOnly,
  onChange,
  labelId,
  label,
  invalid,
}: {
  value: unknown;
  readOnly: boolean;
  onChange: (days: string[]) => void;
  labelId?: string;
  label?: string;
  invalid?: boolean;
}) {
  const { locale } = useI18n();
  const names = weekdayNames(locale);
  const on = new Set(asArray(value).map(String));
  return (
    // a setting's own row is already its labelled group; a table cell names its own
    <div
      role={labelId ? undefined : "group"}
      className="mkt-rules__days"
      aria-label={labelId ? undefined : label}
      aria-invalid={invalid || undefined}
    >
      {WEEKDAYS.map((d, i) => (
        <button
          key={d}
          type="button"
          className="mkt-rules__day"
          aria-pressed={on.has(d)}
          aria-label={names[i].long}
          title={names[i].long}
          disabled={readOnly}
          onClick={() => onChange(WEEKDAYS.filter((x) => (x === d ? !on.has(d) : on.has(x))))}
        >
          {names[i].short}
        </button>
      ))}
    </div>
  );
}

// ---- tables ------------------------------------------------------------

function columnsOf(f: RuleField, t: T): Column[] {
  if (baseKind(f.kind) === "map") {
    return [
      { name: "key", label: t("settings.rules.mapKey"), kind: "str", optional: false },
      { name: "values", label: t("settings.rules.mapValues"), kind: "list", optional: false },
    ].map((c) => ({ ...c, control: c.kind === "list" ? "list" : "text", unit: null }));
  }
  const rows = [...asArray(f.default), ...asArray(f.value)];
  const sample = rows[0];
  const cell = (r: unknown, c: string, j: number) =>
    Array.isArray(r)
      ? r[j]
      : r && typeof r === "object"
        ? (r as Record<string, unknown>)[c]
        : undefined;
  return (f.columns ?? []).map((c, j) => {
    let kind = f.column_kinds?.[j];
    if (!kind) {
      const s = cell(sample, c, j);
      kind =
        f.time_columns?.includes(j) || /^(start|end|from|until)$/.test(c)
          ? "time"
          : typeof s === "number"
            ? "float?"
            : typeof s === "boolean"
              ? "bool"
              : Array.isArray(s)
                ? "list"
                : "str";
    }
    const samples = rows.map((r) => cell(r, c, j));
    return {
      name: c,
      label: c.replace(/_/g, " "),
      kind,
      optional: !!f.column_optional?.[j],
      control: columnControl(c, kind, samples),
      unit: baseKind(kind) === "int" || baseKind(kind) === "float" ? unitOf(c) : null,
    };
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

const NONE = Symbol("omit");

function parseCell(col: Column, s: string): unknown {
  const k = baseKind(col.kind);
  const opt = isOptional(col.kind);
  // an optional column left empty is left out, so its default applies
  if (s.trim() === "" && col.optional) return NONE;
  if (k === "int" || k === "float") return s.trim() === "" ? null : Number(s);
  if (k === "bool") return s === "true";
  if (k === "list" || k === "words") return splitItems(s);
  if (k === "str" && opt && s === "") return null;
  return s;
}

/** "live: row 2 url: must be …" → the cell it names ("1:1"). */
function serverCell(message: string | undefined, cols: Column[]): string | null {
  const m = message && /row (\d+)(?: (\w+))?:/.exec(message);
  if (!m) return null;
  const j = m[2] ? cols.findIndex((c) => c.name === m[2]) : -1;
  return `${Number(m[1]) - 1}:${j}`;
}

export function tableProblems(f: RuleField, value: unknown, t: T) {
  const cols = columnsOf(f, t);
  return { cols, cells: baseKind(f.kind) === "rows" ? cellProblems(cols, value) : {} };
}

function TableEditor({
  f,
  value,
  labelId,
  describedBy,
  readOnly,
  onChange,
  serverError,
}: EditorProps & { serverError?: string }) {
  const { t } = useI18n();
  const isMap = baseKind(f.kind) === "map";
  const { cols, cells: problems } = tableProblems(f, value, t);
  // only rows of named fields have optional columns
  const named = rowsAreObjects(f) || !!f.column_optional;
  const tbody = useRef<HTMLTableSectionElement>(null);
  const focusNew = useRef(false);
  const badCell = serverCell(serverError, cols);

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
        if (k.trim()) o[k.trim()] = splitItems(vals);
      }
      return o;
    }
    return raw.map((row) => {
      const typed = row.map((s, j) => parseCell(cols[j], s));
      if (named) {
        return Object.fromEntries(
          cols.flatMap((c, j) => (typed[j] === NONE ? [] : [[c.name, typed[j]]]))
        );
      }
      return typed.map((x) => (x === NONE ? null : x));
    });
  };
  const [rows, setRows] = useDraft(value, format, parse);
  const commit = (next: string[][]) => {
    setRows(next);
    onChange(parse(next));
  };
  const setCell = (i: number, j: number, s: string) =>
    commit(rows.map((r, ri) => (ri === i ? r.map((c, ci) => (ci === j ? s : c)) : r)));

  useEffect(() => {
    if (!focusNew.current) return;
    focusNew.current = false;
    tbody.current?.lastElementChild?.querySelector<HTMLElement>("input, button")?.focus();
  }, [rows.length]);

  const startCol = cols.findIndex((c) => /^(start|from)$/.test(c.name) && c.control === "time");
  const endCol = cols.findIndex((c) => /^(end|until)$/.test(c.name) && c.control === "time");

  const control = (c: Column, s: string, i: number, j: number, errId?: string) => {
    const common = {
      "aria-label": c.label,
      "aria-describedby": errId,
      "aria-invalid": errId ? true : undefined,
    };
    switch (c.control) {
      case "bool":
        return (
          <Toggle
            {...common}
            checked={s === "true"}
            disabled={readOnly}
            onChange={(e) => setCell(i, j, e.target.checked ? "true" : "false")}
          />
        );
      case "days":
        return (
          <DayChips
            label={c.label}
            value={splitItems(s)}
            readOnly={readOnly}
            invalid={!!errId}
            onChange={(d) => setCell(i, j, d.join(", "))}
          />
        );
      case "time":
        return (
          <TimeInput
            {...common}
            className="mkt-rules__cell mkt-rules__cell--short"
            value={s}
            readOnly={readOnly}
            onChange={(v) => setCell(i, j, v)}
          />
        );
      case "number":
        return (
          <span className="mkt-rules__cellnum">
            <input
              {...common}
              className="mkt-rules__cell mkt-rules__cell--short"
              type="number"
              step={baseKind(c.kind) === "int" ? 1 : "any"}
              value={s}
              readOnly={readOnly}
              onChange={(e) => setCell(i, j, e.target.value)}
            />
            {c.unit && (
              <span className="mkt-rules__unit">{t(`settings.rules.unit.${c.unit}`)}</span>
            )}
          </span>
        );
      default:
        return (
          <input
            {...common}
            className={`mkt-rules__cell${c.control === "url" ? " mkt-rules__cell--url" : ""}`}
            type={c.control === "url" ? "url" : "text"}
            inputMode={c.control === "url" ? "url" : undefined}
            dir={c.control === "url" ? "ltr" : "auto"}
            value={s}
            readOnly={readOnly}
            onChange={(e) => setCell(i, j, e.target.value)}
          />
        );
    }
  };

  return (
    <>
      <div className="mkt-rules__tablewrap">
        <table
          className="mkt-rules__table"
          aria-labelledby={labelId}
          aria-describedby={describedBy}
        >
          <thead>
            <tr>
              {cols.map((c) => (
                <th key={c.name} scope="col">
                  {c.label}
                </th>
              ))}
              {!readOnly && (
                <th scope="col">
                  <span className="mkt-rules__sr">{t("settings.rules.rowActions")}</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody ref={tbody}>
            {rows.map((cells, i) => (
              <tr key={i}>
                {cells.map((s, j) => {
                  const c = cols[j];
                  const p: Problem | undefined = problems[`${i}:${j}`];
                  const fromServer = !p && badCell === `${i}:${j}` ? serverError : undefined;
                  const errId = p || fromServer ? `${labelId}-r${i}c${j}-err` : undefined;
                  const wraps = j === endCol && startCol >= 0 && wrapsMidnight(cells[startCol], s);
                  return (
                    <td key={c.name} data-label={c.label}>
                      {control(c, s, i, j, errId)}
                      {errId && (
                        <span className="mkt-rules__cellerr" id={errId}>
                          {p ? t(p.key, p.vars) : fromServer}
                        </span>
                      )}
                      {wraps && (
                        <span className="mkt-rules__hint">{t("settings.rules.wraps")}</span>
                      )}
                    </td>
                  );
                })}
                {!readOnly && (
                  <td className="mkt-rules__cell-x">
                    <span className="mkt-rules__rowbtns">
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={t("settings.rules.duplicateRow")}
                        title={t("settings.rules.duplicateRow")}
                        onClick={() =>
                          commit([...rows.slice(0, i + 1), [...cells], ...rows.slice(i + 1)])
                        }
                      >
                        <span aria-hidden="true">⧉</span>
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        aria-label={t("settings.rules.removeRow")}
                        title={t("settings.rules.removeRow")}
                        onClick={() => commit(rows.filter((_, ri) => ri !== i))}
                      >
                        <span aria-hidden="true">✕</span>
                      </Button>
                    </span>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
        {rows.length === 0 && (
          <p className="mkt-rules__hint mkt-rules__empty">{t("settings.rules.emptyTable")}</p>
        )}
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

const humanChoice = (c: string) => c.replace(/[_-]+/g, " ");

export function Editor(
  props: EditorProps & { serverError?: string; show: (v: unknown) => string }
) {
  const { t, locale } = useI18n();
  const { f, value, labelId, describedBy, invalid, readOnly, onChange } = props;
  const opt = isOptional(f.kind);
  const aria = {
    "aria-labelledby": labelId,
    "aria-describedby": describedBy,
    "aria-invalid": invalid || undefined,
  };
  switch (controlOf(f)) {
    case "bool":
      return (
        <Toggle
          {...aria}
          checked={value === true}
          disabled={readOnly}
          label={
            <span aria-hidden="true">
              {value ? t("settings.rules.on") : t("settings.rules.off")}
            </span>
          }
          onChange={(e) => onChange(e.target.checked)}
        />
      );
    case "select":
    case "weekday": {
      const choices = f.choices ?? [];
      const current = String(value ?? "");
      const names = weekdayNames(locale);
      const label = (c: string) => {
        const d = (WEEKDAYS as readonly string[]).indexOf(c);
        return d >= 0 ? names[d].long : humanChoice(c);
      };
      const options = choices.includes(current) ? choices : [current, ...choices];
      return (
        <Select
          {...aria}
          className="mkt-rules__select"
          value={current}
          disabled={readOnly}
          onChange={(e) => onChange(e.target.value)}
          options={options.map((c) => ({ value: c, label: label(c) }))}
        />
      );
    }
    case "number":
      return <NumberEditor {...props} />;
    case "time":
      return (
        <TimeInput
          {...aria}
          className="mkt-rules__input mkt-rules__input--short"
          value={String(value ?? "")}
          readOnly={readOnly}
          onChange={(v) => onChange(v === "" && opt ? null : v)}
        />
      );
    case "text":
    case "url":
      return (
        <input
          {...aria}
          type={controlOf(f) === "url" ? "url" : "text"}
          className="mkt-rules__input"
          dir={controlOf(f) === "url" ? "ltr" : "auto"}
          value={String(value ?? "")}
          readOnly={readOnly}
          placeholder={opt ? t("settings.rules.empty") : undefined}
          onChange={(e) => onChange(opt && e.target.value === "" ? null : e.target.value)}
        />
      );
    case "days":
      return (
        <DayChips
          labelId={labelId}
          value={value}
          readOnly={readOnly}
          invalid={invalid}
          onChange={onChange}
        />
      );
    case "chips":
      return <ChipsEditor {...props} />;
    case "lines":
      return <LinesEditor {...props} />;
    case "table":
      return <TableEditor {...props} />;
    default:
      return (
        <p className="mkt-rules__hint">
          {t("settings.rules.notEditable")} <span dir="auto">{props.show(value)}</span>
        </p>
      );
  }
}
