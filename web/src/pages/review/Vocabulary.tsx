// /review/vocabulary — what fixes taught (Epic 52, story 52.10): search, filter, add, edit
// variants, delete, import and export. Home admins only; the API's 403 is the rule.
import { useCallback, useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { ApiError, uploadWithProgress } from "../../lib/api";
import { reviewApi, type Vocab } from "../../lib/reviewApi";
import "../../styles/review.css";

export function Vocabulary() {
  const { t } = useI18n();
  const [items, setItems] = useState<Vocab[] | null>(null);
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("");
  const [msg, setMsg] = useState<string | null>(null);
  const [term, setTerm] = useState("");
  const [lang, setLang] = useState("ar");
  const [newKind, setNewKind] = useState<Vocab["kind"]>("name");
  const [library, setLibrary] = useState("");

  const load = useCallback(async () => {
    try {
      setItems((await reviewApi.vocabulary({ q, kind })).items);
    } catch (e) {
      setMsg(
        e instanceof ApiError && e.status === 403 ? t("review.adminsOnly") : t("review.loadFailed")
      );
    }
  }, [q, kind, t]);
  useEffect(() => void load(), [load]);

  const add = async () => {
    try {
      await reviewApi.addVocab({
        scope: library ? "library" : "home",
        library_id: library || undefined,
        language: lang,
        term,
        kind: newKind,
      });
      setTerm("");
      await load();
    } catch {
      setMsg(t("vocabulary.addFailed"));
    }
  };

  const importCsv = async (file: File) => {
    try {
      const res = await uploadWithProgress<{
        added: number;
        bad: Array<{ line: number; reason: string }>;
      }>(
        `/api/vocabulary/import?scope=${library ? "library" : "home"}${library ? `&library=${library}` : ""}`,
        file,
        undefined,
        "text/csv"
      );
      setMsg(
        t("vocabulary.imported", { added: res.added, bad: res.bad.length }) +
          (res.bad.length ? " " + res.bad.map((b) => `${b.line}: ${b.reason}`).join("; ") : "")
      );
      await load();
    } catch {
      setMsg(t("vocabulary.importFailed"));
    }
  };

  return (
    <main className="mkt-review" aria-labelledby="vocab-title">
      <h1 id="vocab-title">{t("vocabulary.title")}</h1>
      <p>{t("vocabulary.intro")}</p>
      <div className="mkt-review-filters">
        <label>
          {t("vocabulary.search")}
          <input value={q} onChange={(e) => setQ(e.target.value)} dir="auto" />
        </label>
        <label>
          {t("vocabulary.kind")}
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">{t("review.filters.any")}</option>
            {(["word", "name", "term"] as const).map((k) => (
              <option key={k} value={k}>
                {t(`vocabulary.kinds.${k}`)}
              </option>
            ))}
          </select>
        </label>
        <a href={reviewApi.vocabExportUrl()}>{t("vocabulary.export")}</a>
        <a href={reviewApi.correctionsExportUrl()}>{t("vocabulary.exportCorrections")}</a>
      </div>
      <form
        className="mkt-review-filters"
        onSubmit={(e) => {
          e.preventDefault();
          void add();
        }}
      >
        <label>
          {t("vocabulary.term")}
          <input value={term} onChange={(e) => setTerm(e.target.value)} dir="auto" required />
        </label>
        <label>
          {t("vocabulary.language")}
          <input value={lang} onChange={(e) => setLang(e.target.value)} size={4} dir="ltr" />
        </label>
        <label>
          {t("vocabulary.kind")}
          <select value={newKind} onChange={(e) => setNewKind(e.target.value as Vocab["kind"])}>
            {(["word", "name", "term"] as const).map((k) => (
              <option key={k} value={k}>
                {t(`vocabulary.kinds.${k}`)}
              </option>
            ))}
          </select>
        </label>
        <label>
          {t("vocabulary.library")}
          <input
            value={library}
            onChange={(e) => setLibrary(e.target.value)}
            dir="ltr"
            placeholder={t("vocabulary.home")}
          />
        </label>
        <button type="submit">{t("vocabulary.add")}</button>
        <label>
          {t("vocabulary.import")}
          <input
            type="file"
            accept=".csv,text/csv"
            onChange={(e) => e.target.files?.[0] && void importCsv(e.target.files[0])}
          />
        </label>
      </form>
      {msg && <p role="status">{msg}</p>}
      {items && (
        <table className="mkt-review-table">
          <thead>
            <tr>
              <th>{t("vocabulary.term")}</th>
              <th>{t("vocabulary.kind")}</th>
              <th>{t("vocabulary.language")}</th>
              <th>{t("vocabulary.variants")}</th>
              <th>{t("vocabulary.uses")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {items.map((v) => (
              <tr key={v.id}>
                <td>
                  <bdi dir="auto">{v.term}</bdi>
                </td>
                <td>{t(`vocabulary.kinds.${v.kind}`)}</td>
                <td>{v.language}</td>
                <td>
                  <input
                    aria-label={t("vocabulary.variants")}
                    defaultValue={v.variants.join(", ")}
                    dir="auto"
                    onBlur={(e) => {
                      const variants = e.target.value
                        .split(",")
                        .map((s) => s.trim())
                        .filter(Boolean);
                      if (variants.join(",") !== v.variants.join(","))
                        void reviewApi.editVocab(v.id, { term: v.term, variants }).then(load);
                    }}
                  />
                </td>
                <td>{v.uses}</td>
                <td>
                  <button type="button" onClick={() => void reviewApi.deleteVocab(v.id).then(load)}>
                    {t("vocabulary.delete")}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </main>
  );
}
