// /review/languages — the language packs (Epic 52, story 52.14): for each language heard in
// the home's transcripts, its pack and its Epic 51 component, or "fallback in use". The
// switches are Epic 51's (install, remove), called from here.
import { useCallback, useEffect, useState } from "react";
import { useI18n } from "../../lib/i18n";
import { ApiError, api } from "../../lib/api";
import { reviewApi, type LanguagePacksView } from "../../lib/reviewApi";
import "../../styles/review.css";

/** The component of a language's data pack. */
export function packComponent(code: string): string {
  return code === "ar" || code === "en" ? `lang.${code}` : `lang.hunspell.${code}`;
}

export function LanguagePacks() {
  const { t } = useI18n();
  const [view, setView] = useState<LanguagePacksView | null>(null);
  const [msg, setMsg] = useState<string | null>(null);
  const load = useCallback(async () => {
    try {
      setView(await reviewApi.languagePacks());
    } catch (e) {
      setMsg(
        e instanceof ApiError && e.status === 403 ? t("review.adminsOnly") : t("review.loadFailed")
      );
    }
  }, [t]);
  useEffect(() => void load(), [load]);

  const act = async (id: string, install: boolean) => {
    try {
      if (install) await api.post(`/api/components/${id}/install`);
      else await api.delete(`/api/components/${id}`);
      setMsg(t(install ? "components.progress.installing" : "wordReview.packs.removing"));
      await load();
    } catch (e) {
      setMsg(
        e instanceof ApiError && e.problem.code === "component_required_by"
          ? t("wordReview.packs.required")
          : t("review.actionFailed")
      );
    }
  };

  const languages = Object.keys(view?.heard ?? {}).sort();
  return (
    <main className="mkt-review" aria-labelledby="packs-title">
      <h1 id="packs-title">{t("wordReview.packs.title")}</h1>
      <p>{t("wordReview.packs.intro")}</p>
      {msg && <p role="status">{msg}</p>}
      {view && (
        <table className="mkt-review-table">
          <thead>
            <tr>
              <th>{t("wordReview.packs.language")}</th>
              <th>{t("wordReview.packs.transcripts")}</th>
              <th>{t("wordReview.packs.pack")}</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {languages.map((code) => {
              const base = code.split("-")[0];
              const row = view.packs.find((p) => p.code === base);
              const comp = view.components?.find((c) => c.id === packComponent(base));
              const loaded = row?.state === "loaded";
              return (
                <tr key={code}>
                  <td>{code}</td>
                  <td>{view.heard[code]}</td>
                  <td>
                    {loaded
                      ? t("wordReview.packs.loaded", { version: row?.version ?? "" })
                      : comp
                        ? t("wordReview.packs.notInstalled")
                        : t("wordReview.packs.fallback")}
                    {comp?.phase && <small> · {t(`components.phase.${comp.phase}`)}</small>}
                  </td>
                  <td>
                    {comp && (
                      <button type="button" onClick={() => void act(comp.id, !loaded)}>
                        {loaded ? t("wordReview.packs.remove") : t("wordReview.packs.install")}
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </main>
  );
}
