// Add library / Edit library: a name and a list of folders, each typed by hand or
// chosen with the folder picker. Edit also shows the library's advanced settings
// as JSON. The API checks every folder; its 422 field errors (`roots[i]`, `name`,
// `settings`) are shown next to the field they name.
import { useState } from "react";
import { Modal } from "@ds/components/Modal/Modal";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Textarea } from "@ds/components/Textarea/Textarea";
import { useToast } from "@ds/components/Toast/Toast";
import {
  fieldErrors,
  libraryAdminApi,
  problemMessage,
  type ManagedLibrary,
} from "../../../lib/libraryAdminApi";
import { useI18n } from "../../../lib/i18n";
import { FolderPicker } from "./FolderPicker";

interface Row {
  key: number;
  path: string;
}

let nextKey = 1;
const row = (path = ""): Row => ({ key: nextKey++, path });

/** The folders to send (trimmed, no blanks, no repeats) and each one's row index. */
export function cleanRoots(rows: string[]): { roots: string[]; rowOf: number[] } {
  const roots: string[] = [];
  const rowOf: number[] = [];
  rows.forEach((r, i) => {
    const p = r.trim();
    if (p === "" || roots.includes(p)) return;
    roots.push(p);
    rowOf.push(i);
  });
  return { roots, rowOf };
}

const prettySettings = (s: ManagedLibrary["settings"]) => JSON.stringify(s ?? {}, null, 2);

export function LibraryForm({
  library,
  onClose,
  onSaved,
}: {
  /** the library to edit; none: a new library */
  library?: ManagedLibrary;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const toast = useToast();
  const editing = !!library;
  const [name, setName] = useState(library?.name ?? "");
  const [rows, setRows] = useState<Row[]>(() =>
    library && library.roots.length > 0 ? library.roots.map((r) => row(r.path)) : [row()]
  );
  const [picking, setPicking] = useState<number | null>(null);
  const [settingsText, setSettingsText] = useState(() => prettySettings(library?.settings));
  const [busy, setBusy] = useState(false);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [formError, setFormError] = useState<string | null>(null);

  const { roots, rowOf } = cleanRoots(rows.map((r) => r.path));

  let settings: Record<string, unknown> | null = null;
  let settingsError: string | null = null;
  try {
    const v: unknown = JSON.parse(settingsText);
    if (v && typeof v === "object" && !Array.isArray(v)) settings = v as Record<string, unknown>;
    else settingsError = t("libraryAdmin.form.settingsObject");
  } catch {
    settingsError = t("libraryAdmin.form.settingsInvalid");
  }
  const settingsChanged = editing && settingsText.trim() !== prettySettings(library.settings);

  const valid = name.trim() !== "" && roots.length > 0 && (!settingsChanged || settings !== null);

  function setPath(key: number, path: string) {
    setRows((rs) => rs.map((r) => (r.key === key ? { ...r, path } : r)));
  }

  async function submit() {
    if (!valid) return;
    setBusy(true);
    setErrors({});
    setFormError(null);
    try {
      let warnings: string[] | undefined;
      if (editing) {
        const body: { name?: string; roots?: string[]; settings?: Record<string, unknown> } = {};
        if (name.trim() !== library.name) body.name = name.trim();
        const before = library.roots.map((r) => r.path);
        if (roots.length !== before.length || roots.some((p, i) => p !== before[i])) {
          body.roots = roots;
        }
        if (settingsChanged && settings) body.settings = settings;
        if (Object.keys(body).length > 0) {
          warnings = (await libraryAdminApi.update(library.id, body))?.warnings;
        }
        toast.show({ tone: "success", message: t("libraryAdmin.form.saved") });
      } else {
        warnings = (await libraryAdminApi.create({ name: name.trim(), roots }))?.warnings;
        toast.show({ tone: "success", message: t("libraryAdmin.form.created") });
      }
      for (const w of warnings ?? []) toast.show({ tone: "warn", message: w });
      onSaved();
    } catch (e) {
      // each field error beside its field; one with no field here (or a refusal
      // with none, such as two libraries' folders overlapping) shows on top
      const next: Record<string, string> = {};
      const loose: string[] = [];
      for (const f of fieldErrors(e)) {
        const m = /^roots\[(\d+)\]$/.exec(f.field);
        const r = m ? rows[rowOf[Number(m[1])] ?? -1] : undefined;
        if (r) next[`row:${r.key}`] = f.message;
        else if (f.field === "name" || f.field === "settings") next[f.field] = f.message;
        else loose.push(f.message);
      }
      setErrors(next);
      if (loose.length > 0) setFormError(loose.join(" "));
      else if (Object.keys(next).length === 0) setFormError(problemMessage(e, t("common.error")));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open
      onClose={onClose}
      className="mkt-libadmin__modal"
      title={
        editing
          ? t("libraryAdmin.form.editTitle", { name: library.name })
          : t("libraryAdmin.form.addTitle")
      }
      footer={
        <>
          <Button type="button" variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button type="button" onClick={() => void submit()} loading={busy} disabled={!valid}>
            {editing ? t("common.save") : t("libraryAdmin.form.add")}
          </Button>
        </>
      }
    >
      <form
        className="mkt-form"
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        {formError && (
          <p className="mkt-error" role="alert">
            {formError}
          </p>
        )}
        <Input
          label={t("libraryAdmin.form.name")}
          value={name}
          onChange={(e) => setName(e.target.value)}
          error={errors.name}
          autoComplete="off"
          required
        />
        <fieldset className="mkt-libadmin__folders">
          <legend>{t("libraryAdmin.form.folders")}</legend>
          <p className="mkt-muted mkt-libadmin__small">{t("libraryAdmin.form.foldersHelp")}</p>
          {rows.map((r, i) => (
            <div key={r.key} className="mkt-libadmin__folder-row">
              <div className="mkt-libadmin__folder-fields">
                <Input
                  label={t("libraryAdmin.form.folder", { n: i + 1 })}
                  value={r.path}
                  dir="ltr"
                  spellCheck={false}
                  autoComplete="off"
                  placeholder="/Volumes/…"
                  onChange={(e) => setPath(r.key, e.target.value)}
                  error={errors[`row:${r.key}`]}
                />
                <Button
                  type="button"
                  size="sm"
                  variant="secondary"
                  onClick={() => setPicking(picking === r.key ? null : r.key)}
                  aria-expanded={picking === r.key}
                >
                  {t("libraryAdmin.form.browse")}
                </Button>
                <Button
                  type="button"
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    setRows((rs) => (rs.length > 1 ? rs.filter((x) => x.key !== r.key) : [row()]));
                    if (picking === r.key) setPicking(null);
                  }}
                  aria-label={t("libraryAdmin.form.removeFolder", { n: i + 1 })}
                >
                  {t("libraryAdmin.form.remove")}
                </Button>
              </div>
              {picking === r.key && (
                <FolderPicker
                  start={r.path}
                  onCancel={() => setPicking(null)}
                  onPick={(p) => {
                    setPath(r.key, p);
                    setPicking(null);
                  }}
                />
              )}
            </div>
          ))}
          <Button
            type="button"
            size="sm"
            variant="secondary"
            onClick={() => setRows((rs) => [...rs, row()])}
          >
            {t("libraryAdmin.form.addFolder")}
          </Button>
        </fieldset>
        {editing && (
          <details className="mkt-libadmin__advanced">
            <summary>{t("libraryAdmin.form.advanced")}</summary>
            <p className="mkt-muted mkt-libadmin__small">{t("libraryAdmin.form.advancedHelp")}</p>
            <Textarea
              label={t("libraryAdmin.form.settings")}
              value={settingsText}
              dir="ltr"
              spellCheck={false}
              rows={6}
              onChange={(e) => setSettingsText(e.target.value)}
              error={(settingsChanged && settingsError) || errors.settings}
            />
          </details>
        )}
        <button type="submit" hidden tabIndex={-1} />
      </form>
    </Modal>
  );
}
