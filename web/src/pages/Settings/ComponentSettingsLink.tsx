// Epic 51 (story 51.10): a component row's Settings link. A component owns rules.*
// sections and server.toml keys (the catalog's `settings`, which the list view carries as
// it is); the first rules section opens its form on the Rules tab. The server.toml keys and
// a key the database overrides show in the drawer (ComponentDrawer).
import { Link } from "react-router-dom";
import type { ComponentView } from "../../lib/componentsApi";
import { useI18n } from "../../lib/i18n";

export function ComponentSettingsLink({ view, name }: { view: ComponentView; name: string }) {
  const { t } = useI18n();
  const section = view.settings.find((s) => s.startsWith("rules."));
  if (!section) return null;
  return (
    <Link
      className="mkt-comp-row__settings"
      to={`/settings/rules#${section.slice("rules.".length)}`}
      aria-label={t("components.row.settingsOf", { name })}
    >
      {t("components.row.settings")}
    </Link>
  );
}

/** Story 54.44: a component a plugin implements links to that plugin's page (which links
 * back here). A part of Maktaba itself has no plugin, so no link. */
export function ComponentPluginLink({ view, name }: { view: ComponentView; name: string }) {
  const { t } = useI18n();
  if (!view.plugin) return null;
  return (
    <Link
      className="mkt-comp-row__settings"
      to={`/settings/plugins/${encodeURIComponent(view.plugin)}`}
      aria-label={t("components.row.pluginOf", { name })}
      data-testid={`component-plugin-${view.id}`}
    >
      {t("components.row.plugin")}
    </Link>
  );
}
