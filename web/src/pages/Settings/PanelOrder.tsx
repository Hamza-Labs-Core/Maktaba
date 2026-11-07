// Settings → Plugins → Panel order (story 54.20 §1): pages show at most three plugin panels,
// in the order the admin sets here; a plugin not ordered comes after, in install order.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import { pluginWord, pluginsApi, type PanelPlugin } from "../../lib/pluginsApi";
import { refusalWords } from "./PluginsSection";

export function PanelOrder() {
  const { t, locale } = useI18n();
  const [list, setList] = useState<PanelPlugin[] | null>(null);
  const [note, setNote] = useState("");
  const load = useCallback(async () => {
    try {
      setList((await pluginsApi.panelOrder()).plugins ?? []);
    } catch (e) {
      setNote(refusalWords(t, e));
    }
  }, [t]);
  useEffect(() => {
    void load();
  }, [load]);
  if (!list || list.length < 2) return null;
  const move = async (i: number, by: -1 | 1) => {
    const next = [...list];
    const j = i + by;
    if (j < 0 || j >= next.length) return;
    [next[i], next[j]] = [next[j]!, next[i]!];
    setList(next);
    setNote("");
    try {
      await pluginsApi.setPanelOrder(next.map((p) => p.id));
      setNote(t("plugins.panels.order.saved"));
    } catch (e) {
      setNote(refusalWords(t, e));
      void load();
    }
  };
  return (
    <section aria-labelledby="pl-panel-order" className="mkt-plugins__policy">
      <h3 id="pl-panel-order">{t("plugins.panels.order.title")}</h3>
      <p className="mkt-plugins__hint">{t("plugins.panels.order.explain")}</p>
      <ol className="mkt-plugins__order">
        {list.map((p, i) => {
          const name = pluginWord(p.names.name, locale) || p.id;
          return (
            <li key={p.id}>
              <span dir="auto">{name}</span>
              <Button
                variant="ghost"
                disabled={i === 0}
                aria-label={t("plugins.panels.order.up", { name })}
                onClick={() => void move(i, -1)}
              >
                ↑
              </Button>
              <Button
                variant="ghost"
                disabled={i === list.length - 1}
                aria-label={t("plugins.panels.order.down", { name })}
                onClick={() => void move(i, 1)}
              >
                ↓
              </Button>
            </li>
          );
        })}
      </ol>
      {note && <p role="status">{note}</p>}
    </section>
  );
}
